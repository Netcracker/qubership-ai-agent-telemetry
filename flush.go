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
		var rec otellog.Record
		rec.SetTimestamp(ev.TS)
		rec.SetObservedTimestamp(time.Now().UTC())
		rec.SetBody(otellog.StringValue("skill_executed"))
		rec.AddAttributes(
			otellog.String("agent", ev.Agent),
			otellog.String("session.id", ev.SessionID),
			otellog.String("repo.remote", ev.RepoRemote),
			otellog.String("skill.name", ev.Skill),
		)
		logger.Emit(ctx, rec)
		sentNames = append(sentNames, n)
	}

	// Shutdown flushes the exporter; export errors surface via exportErr.
	_ = provider.Shutdown(ctx)
	if exportErr != nil {
		return 0, exportErr
	}

	for _, n := range sentNames {
		_ = s.Remove(n)
	}
	return len(sentNames), nil
}
