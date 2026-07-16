package main

import (
	"context"
	"crypto/tls"
	"errors"
	"path/filepath"
	"runtime"
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

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Capture export errors: SimpleProcessor routes them to the global handler.
	var exportErr error
	// NOTE: this mutates the process-global OTel error handler. It is safe here
	// because the per-machine flush lock serializes flushes and this binary is a
	// short-lived single-flush CLI. A long-running process linking this code could
	// see the OTel sync.Once delegate behavior interfere with this handler.
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(e error) { exportErr = e }))

	opts := []otlploghttp.Option{otlploghttp.WithEndpointURL(endpoint)}
	if token != "" {
		opts = append(opts, otlploghttp.WithHeaders(map[string]string{"Authorization": "Bearer " + token}))
	}
	if tlsConfig != nil {
		opts = append(opts, otlploghttp.WithTLSClientConfig(tlsConfig))
	}
	exp, err := otlploghttp.New(ctx, opts...)
	if err != nil {
		recordLastDeliveryError(s, err)
		return 0, err
	}
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
	for _, n := range names {
		ev, rerr := s.Read(n)
		if rerr != nil {
			continue // skip unreadable file; do not fail the whole batch
		}
		rec, rerr := eventRecord(ev, time.Now().UTC())
		if rerr != nil {
			continue
		}
		logger.Emit(ctx, rec)
		sentNames = append(sentNames, n)
	}

	// Shutdown flushes the exporter; export errors surface via exportErr.
	_ = provider.Shutdown(ctx)
	if exportErr != nil {
		recordLastDeliveryError(s, exportErr)
		return 0, exportErr
	}

	for _, n := range sentNames {
		_ = s.Remove(n)
	}
	clearLastDeliveryError(s)
	return len(sentNames), nil
}

func eventRecord(ev TelemetryEvent, observed time.Time) (otellog.Record, error) {
	if err := validateSerializableEvent(ev); err != nil {
		return otellog.Record{}, err
	}
	var rec otellog.Record
	rec.SetTimestamp(ev.TS)
	rec.SetObservedTimestamp(observed)
	rec.SetBody(otellog.StringValue(string(ev.EventName)))
	rec.AddAttributes(
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
