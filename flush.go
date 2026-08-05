package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
)

var errLockBusy = errors.New("flush lock busy")

const maxFlushErrors = 8

type logExporterFactory func(context.Context, string, string, *tls.Config) (sdklog.Exporter, error)

// batchingExporter accumulates Export calls and sends one inner.Export on
// Shutdown. SimpleProcessor exports per Emit; this restores the
// design's single OTLP batch per flush without BatchProcessor poll loops.
type batchingExporter struct {
	inner  sdklog.Exporter
	mu     sync.Mutex
	buffer []sdklog.Record
}

func newBatchingExporter(inner sdklog.Exporter) *batchingExporter {
	return &batchingExporter{inner: inner}
}

func (b *batchingExporter) Export(_ context.Context, records []sdklog.Record) error {
	if len(records) == 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// SimpleProcessor exports a pooled slice and clears it after Export returns;
	// clone so buffered records survive until Shutdown.
	for i := range records {
		b.buffer = append(b.buffer, records[i].Clone())
	}
	return nil
}

func (b *batchingExporter) Shutdown(ctx context.Context) error {
	flushErr := b.flush(ctx)
	shutErr := b.inner.Shutdown(ctx)
	return errors.Join(flushErr, shutErr)
}

func (b *batchingExporter) ForceFlush(ctx context.Context) error {
	return b.flush(ctx)
}

func (b *batchingExporter) flush(ctx context.Context) error {
	b.mu.Lock()
	records := b.buffer
	b.buffer = nil
	b.mu.Unlock()
	if len(records) == 0 {
		return nil
	}
	return b.inner.Export(ctx, records)
}

type deliveryResolver struct {
	Endpoint func() (string, error)
	TLS      func() (*tls.Config, error)
	Token    func() string
	Timeout  func() time.Duration
	Exporter logExporterFactory
	Remove   func(string) error
}

type boundedErrors struct {
	errs    []error
	omitted int
}

func (e *boundedErrors) add(err error) {
	if err == nil {
		return
	}
	if len(e.errs) < maxFlushErrors {
		e.errs = append(e.errs, err)
		return
	}
	e.omitted++
}

func (e *boundedErrors) err() error {
	if e.omitted > 0 {
		return errors.Join(append(e.errs, fmt.Errorf("%d additional flush errors omitted", e.omitted))...)
	}
	return errors.Join(e.errs...)
}

// resourceAttrs builds the OTLP resource attributes that describe this install:
// service identity, build version, and the host OS, plus the anonymous machine
// id when one is set. osType is runtime.GOOS, whose values (windows, linux,
// darwin) already match the OpenTelemetry `os.type` semantic convention. These
// describe the producing machine, so they live on the resource rather than on
// each per-event log record.
func resourceAttrs(serviceVersion, osType, machineID string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("service.name", "ai-agent-telemetry"),
		attribute.String("service.version", serviceVersion),
		attribute.String("os.type", osType),
	}
	if machineID != "" {
		attrs = append(attrs, attribute.String("machine.id", machineID))
	}
	return attrs
}

// lockOutbox takes a non-blocking advisory lock for the outbox. The returned
// func releases it. A nil release with errLockBusy means the lock was busy.
func lockOutbox(s *Outbox) (release func(), busy error) {
	fl := flock.New(filepath.Join(s.Dir, ".flush.lock"))
	ok, err := fl.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errLockBusy
	}
	return func() { _ = fl.Unlock() }, nil
}

// Flush sends every buffered event to the OTLP/HTTP endpoint and removes the
// files that were sent. Returns the number of events sent. A non-nil tlsConfig
// adds the configured CA to the transport; nil leaves TLS at its default
// (system trust store). Skips (0, nil) when: endpoint is empty, buffer empty,
// or the lock is held.
func Flush(s *Outbox, endpoint, token string, tlsConfig *tls.Config, timeout time.Duration) (int, error) {
	if endpoint == "" {
		return 0, nil
	}
	names, err := s.List()
	if err != nil {
		return 0, err
	}
	if len(names) == 0 {
		return 0, nil
	}

	release, lockErr := lockOutbox(s)
	if lockErr == errLockBusy {
		return 0, nil
	}
	if lockErr != nil {
		return 0, lockErr
	}
	defer release()

	// Re-list under the lock to avoid sending files a concurrent flush already took.
	names, err = s.List()
	if err != nil || len(names) == 0 {
		return 0, err
	}
	return deliverEvents(s, names, endpoint, token, tlsConfig, timeout, nil, nil, false)
}

// flushExplicit performs a human-requested flush. It validates the outbox
// under its lock before resolving delivery configuration and reports every
// retained event as an error.
func flushExplicit(s *Outbox, resolve deliveryResolver) (int, error) {
	release, err := lockOutbox(s)
	if err != nil {
		return 0, fmt.Errorf("lock outbox: %w", err)
	}
	defer release()

	names, err := s.List()
	if err != nil {
		return 0, fmt.Errorf("list outbox: %w", err)
	}
	if len(names) == 0 {
		return 0, nil
	}

	if resolve.Endpoint == nil {
		return 0, errors.New("collector endpoint resolver is unavailable")
	}
	endpoint, err := resolve.Endpoint()
	if err != nil {
		return 0, fmt.Errorf("resolve collector endpoint: %w", err)
	}
	if endpoint == "" {
		return 0, errors.New("collector endpoint is not configured")
	}

	var tlsConfig *tls.Config
	if resolve.TLS != nil {
		tlsConfig, err = resolve.TLS()
		if err != nil {
			return 0, fmt.Errorf("load collector CA: %w", err)
		}
	}
	var token string
	if resolve.Token != nil {
		token = resolve.Token()
	}
	timeout := defaultFlushTimeout
	if resolve.Timeout != nil {
		timeout = resolve.Timeout()
	}
	return deliverEvents(s, names, endpoint, token, tlsConfig, timeout, resolve.Exporter, resolve.Remove, true)
}

func deliverEvents(
	s *Outbox,
	names []string,
	endpoint, token string,
	tlsConfig *tls.Config,
	timeout time.Duration,
	exporterFactory logExporterFactory,
	remove func(string) error,
	strict bool,
) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Capture export errors: SimpleProcessor routes them to the global handler.
	var exportIssues boundedErrors
	// NOTE: this mutates the process-global OTel error handler. It is safe here
	// because the per-machine flush lock serializes flushes and this binary is a
	// short-lived single-flush CLI. A long-running process linking this code could
	// see the OTel sync.Once delegate behavior interfere with this handler.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		exportIssues.add(fmt.Errorf("export events: %w", err))
	}))

	if exporterFactory == nil {
		exporterFactory = newOTLPLogExporter
	}
	inner, err := exporterFactory(ctx, endpoint, token, tlsConfig)
	if err != nil {
		recordLastDeliveryError(s, err)
		return 0, err
	}
	exp := newBatchingExporter(inner)
	res := resource.NewSchemaless(resourceAttrs(version, runtime.GOOS, resolveMachineID())...)
	provider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewSimpleProcessor(exp)),
		sdklog.WithResource(res),
	)
	// The instrumentation scope duplicates service.* for a self-emitting binary;
	// at least carry the build version so scope.version is not "unknown".
	logger := provider.Logger(
		"ai-agent-telemetry",
		otellog.WithInstrumentationVersion(version),
	)

	sentNames := make([]string, 0, len(names))
	var retainedIssues boundedErrors
	for _, n := range names {
		ev, rerr := s.Read(n)
		if rerr != nil {
			if strict {
				retainedIssues.add(fmt.Errorf("read event %q: %w", n, rerr))
			}
			continue
		}
		rec, rerr := eventRecord(ev, time.Now().UTC(), eventIDForDelivery(ev, n))
		if rerr != nil {
			if strict {
				retainedIssues.add(fmt.Errorf("validate event %q: %w", n, rerr))
			}
			continue
		}
		logger.Emit(ctx, rec)
		sentNames = append(sentNames, n)
	}

	// Shutdown flushes the exporter; the batching decorator exports once here.
	shutdownErr := provider.Shutdown(ctx)
	deliveryErr := exportIssues.err()
	if shutdownErr != nil {
		deliveryErr = errors.Join(deliveryErr, fmt.Errorf("shut down exporter: %w", shutdownErr))
	}
	if deliveryErr != nil {
		if strict {
			retainedIssues.add(deliveryErr)
			err := retainedIssues.err()
			recordLastDeliveryError(s, err)
			return 0, err
		}
		recordLastDeliveryError(s, deliveryErr)
		return 0, deliveryErr
	}

	if remove == nil {
		remove = s.Remove
	}
	for _, n := range sentNames {
		if err := remove(n); strict && err != nil {
			retainedIssues.add(fmt.Errorf("remove delivered event %q: %w", n, err))
		}
	}
	if err := retainedIssues.err(); err != nil {
		recordLastDeliveryError(s, err)
		return len(sentNames), err
	}
	clearLastDeliveryError(s)
	return len(sentNames), nil
}

func newOTLPLogExporter(
	ctx context.Context,
	endpoint, token string,
	tlsConfig *tls.Config,
) (sdklog.Exporter, error) {
	opts := []otlploghttp.Option{otlploghttp.WithEndpointURL(endpoint)}
	if token != "" {
		opts = append(opts, otlploghttp.WithHeaders(map[string]string{"Authorization": "Bearer " + token}))
	}
	if tlsConfig != nil {
		opts = append(opts, otlploghttp.WithTLSClientConfig(tlsConfig))
	}
	return otlploghttp.New(ctx, opts...)
}

func eventRecord(ev TelemetryEvent, observed time.Time, eventID string) (otellog.Record, error) {
	if err := validateSerializableEvent(ev); err != nil {
		return otellog.Record{}, err
	}
	var rec otellog.Record
	rec.SetTimestamp(ev.TS)
	rec.SetObservedTimestamp(observed)
	rec.SetBody(otellog.StringValue(string(ev.EventName)))
	rec.AddAttributes(
		otellog.String("event.id", eventID),
		otellog.String("agent", ev.Agent),
		otellog.String("session.id", ev.SessionID),
		otellog.String("repo.remote", ev.RepoRemote),
	)
	switch payload := ev.Payload.(type) {
	case SkillPayload:
		rec.AddAttributes(otellog.String("skill.name", payload.SkillName))
	case CommandPayload:
		rec.AddAttributes(
			otellog.String("command.name", payload.CommandName),
			otellog.String("command.source", payload.CommandSource),
			otellog.String("command.expansion_type", payload.ExpansionType),
		)
	case MCPPayload:
		if payload.ServerName != "" {
			rec.AddAttributes(otellog.String("mcp.server.name", payload.ServerName))
		}
		rec.AddAttributes(
			otellog.String("mcp.tool.name", payload.ToolName),
			otellog.String("mcp.outcome", string(payload.Outcome)),
		)
		if payload.DurationMS != nil {
			rec.AddAttributes(otellog.Int64("mcp.duration_ms", *payload.DurationMS))
		}
	default:
		return otellog.Record{}, errors.New("unknown telemetry payload")
	}
	return rec, nil
}
