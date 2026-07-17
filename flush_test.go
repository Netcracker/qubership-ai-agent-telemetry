package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	collectlogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	logsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestFlushPreservesSkillOTLPSchema(t *testing.T) {
	records := flushRecords(t, []TelemetryEvent{mustSkillEvent(t, "codex", "s1", "brainstorming")})
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1", len(records))
	}
	if got := records[0].Body.GetStringValue(); got != "skill_executed" {
		t.Fatalf("body = %q", got)
	}
	assertOTLPAttrs(t, records[0].Attributes, map[string]any{
		"agent": "codex", "session.id": "s1", "repo.remote": "",
		"skill.name": "brainstorming",
	})
}

func TestFlushMapsAllTypedPayloads(t *testing.T) {
	duration := int64(42)
	ts := time.Unix(1, 0).UTC()
	skill, _ := newSkillEvent("codex", "s1", "", "", "brainstorming", ts)
	command, _ := newCommandEvent("codex", "s2", "", "", CommandPayload{
		CommandName: "review-pr", CommandSource: "plugin", ExpansionType: "slash_command",
	}, ts)
	mcp, _ := newMCPEvent("codex", "s3", "", "", MCPPayload{
		ServerName: "github", ToolName: "get_issue", Outcome: mcpSucceeded, DurationMS: &duration,
	}, ts)
	cursorMCP, _ := newMCPEvent("cursor", "s4", "", "", MCPPayload{
		ToolName: "search", Outcome: mcpUnknown,
	}, ts)
	records := flushRecords(t, []TelemetryEvent{skill, command, mcp, cursorMCP})
	if len(records) != 4 {
		t.Fatalf("got %d records, want 4", len(records))
	}
	want := []struct {
		body  string
		attrs map[string]any
	}{
		{body: "skill_executed", attrs: map[string]any{
			"agent": "codex", "session.id": "s1", "repo.remote": "", "skill.name": "brainstorming",
		}},
		{body: "command_invoked", attrs: map[string]any{
			"agent": "codex", "session.id": "s2", "repo.remote": "", "command.name": "review-pr",
			"command.source": "plugin", "command.expansion_type": "slash_command",
		}},
		{body: "mcp_tool_executed", attrs: map[string]any{
			"agent": "codex", "session.id": "s3", "repo.remote": "", "mcp.server.name": "github",
			"mcp.tool.name": "get_issue", "mcp.outcome": "succeeded", "mcp.duration_ms": int64(42),
		}},
		{body: "mcp_tool_executed", attrs: map[string]any{
			"agent": "cursor", "session.id": "s4", "repo.remote": "", "mcp.tool.name": "search",
			"mcp.outcome": "unknown",
		}},
	}
	for i, record := range records {
		if got := record.Body.GetStringValue(); got != want[i].body {
			t.Errorf("record %d body = %q, want %q", i, got, want[i].body)
		}
		assertOTLPAttrs(t, record.Attributes, want[i].attrs)
	}
}

func TestFlushMixedBatchSkipsInvalidFileAndPreservesValidOrder(t *testing.T) {
	isolateConfigCache(t)
	outbox := &Outbox{Dir: t.TempDir()}
	first := mustSkillEvent(t, "codex", "s1", "first")
	last := mustSkillEvent(t, "codex", "s2", "last")
	for name, value := range map[string]any{
		"0001.json": first,
		"0002.json": json.RawMessage(`{"schema_version":1,"event_name":"skill_executed","payload":{}}`),
		"0003.json": last,
	} {
		body, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outbox.Dir, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	capture := newOTLPCapture(t)
	defer capture.server.Close()
	sent, err := Flush(outbox, capture.server.URL, "", nil, 2*time.Second)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sent != 2 {
		t.Fatalf("sent = %d, want 2", sent)
	}
	records := capturedRecords(capture.requests)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	for i, want := range []string{"first", "last"} {
		attrs := otlpAttrs(t, records[i].Attributes)
		if got := attrs["skill.name"]; got != want {
			t.Errorf("record %d skill.name = %#v, want %q", i, got, want)
		}
	}
	files, err := outbox.List()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, []string{"0002.json"}) {
		t.Fatalf("remaining files = %v, want [0002.json]", files)
	}
}

func mustSkillEvent(t *testing.T, agent, session, skill string) TelemetryEvent {
	t.Helper()
	ev, err := newSkillEvent(agent, session, "", "", skill, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func flushRecords(t *testing.T, events []TelemetryEvent) []*logsv1.LogRecord {
	t.Helper()
	capture := newOTLPCapture(t)
	defer capture.server.Close()
	s := &Outbox{Dir: t.TempDir()}
	for _, ev := range events {
		if err := s.Enqueue(ev); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := Flush(s, capture.server.URL, "", nil, 2*time.Second); err != nil {
		t.Fatalf("flush: %v", err)
	}
	return capturedRecords(capture.requests)
}

func capturedRecords(requests []*collectlogsv1.ExportLogsServiceRequest) []*logsv1.LogRecord {
	var records []*logsv1.LogRecord
	for _, request := range requests {
		for _, resourceLogs := range request.ResourceLogs {
			for _, scopeLogs := range resourceLogs.ScopeLogs {
				records = append(records, scopeLogs.LogRecords...)
			}
		}
	}
	return records
}

type otlpCapture struct {
	server   *httptest.Server
	bodies   [][]byte
	requests []*collectlogsv1.ExportLogsServiceRequest
}

func newOTLPCapture(t *testing.T) *otlpCapture {
	t.Helper()
	capture := &otlpCapture{}
	capture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var request collectlogsv1.ExportLogsServiceRequest
		if err := proto.Unmarshal(body, &request); err != nil {
			t.Errorf("decode OTLP request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		capture.bodies = append(capture.bodies, body)
		capture.requests = append(capture.requests, &request)
		w.WriteHeader(http.StatusOK)
	}))
	return capture
}

func assertOTLPAttrs(t *testing.T, attrs []*commonv1.KeyValue, want map[string]any) {
	t.Helper()
	got := otlpAttrs(t, attrs)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attributes = %#v, want %#v", got, want)
	}
}

func otlpAttrs(t *testing.T, attrs []*commonv1.KeyValue) map[string]any {
	t.Helper()
	got := make(map[string]any, len(attrs))
	for _, attr := range attrs {
		switch value := attr.Value.Value.(type) {
		case *commonv1.AnyValue_StringValue:
			got[attr.Key] = value.StringValue
		case *commonv1.AnyValue_IntValue:
			got[attr.Key] = value.IntValue
		default:
			t.Fatalf("attribute %q has unexpected value type %T", attr.Key, attr.Value.Value)
		}
	}
	return got
}

func TestResourceAttrsCarriesOSType(t *testing.T) {
	got := map[string]string{}
	for _, kv := range resourceAttrs("1.2.3", "windows", "mid-1") {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	if got["os.type"] != "windows" {
		t.Fatalf("os.type = %q, want windows", got["os.type"])
	}
	if got["service.name"] != "ai-agent-telemetry" {
		t.Fatalf("service.name = %q", got["service.name"])
	}
	if got["service.version"] != "1.2.3" {
		t.Fatalf("service.version = %q", got["service.version"])
	}
	if got["machine.id"] != "mid-1" {
		t.Fatalf("machine.id = %q", got["machine.id"])
	}

	// An empty machine id is omitted, not sent blank.
	for _, kv := range resourceAttrs("v", "linux", "") {
		if string(kv.Key) == "machine.id" {
			t.Fatal("machine.id must be omitted when empty")
		}
	}
}

func seed(t *testing.T, s *Outbox, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		ev := testSkillEvent(t, "codex", "s1", "", "", "s", time.Now().UTC())
		if err := s.Enqueue(ev); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFlushSendsAndClearsOnSuccess(t *testing.T) {
	isolateConfigCache(t)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 3)

	sent, err := Flush(s, srv.URL, "", nil, 2*time.Second)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sent != 3 {
		t.Fatalf("sent = %d, want 3", sent)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("collector received no requests")
	}
	files, _ := s.List()
	if len(files) != 0 {
		t.Fatalf("outbox not cleared: %d files remain", len(files))
	}
}

func TestFlushTrustsConfiguredCA(t *testing.T) {
	isolateConfigCache(t)
	var hits int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	cfg := &tls.Config{RootCAs: pool}

	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 2)

	sent, err := Flush(s, srv.URL, "", cfg, 2*time.Second)
	if err != nil {
		t.Fatalf("flush over TLS with configured CA: %v", err)
	}
	if sent != 2 {
		t.Fatalf("sent = %d, want 2", sent)
	}
	if atomic.LoadInt32(&hits) == 0 {
		t.Fatal("collector received no requests")
	}
}

func TestFlushFailsUntrustedTLS(t *testing.T) {
	isolateConfigCache(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 1)

	// nil tlsConfig => system trust store, which does not trust the test cert.
	_, err := Flush(s, srv.URL, "", nil, 2*time.Second)
	if err == nil {
		t.Fatal("want TLS verification error without the CA")
	}
	files, _ := s.List()
	if len(files) != 1 {
		t.Fatalf("buffer should be intact on TLS failure: %d files", len(files))
	}
}

func TestFlushKeepsBufferOnServerError(t *testing.T) {
	isolateConfigCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 2)

	_, err := Flush(s, srv.URL, "", nil, 2*time.Second)
	if err == nil {
		t.Fatal("want error on server 500")
	}
	files, _ := s.List()
	if len(files) != 2 {
		t.Fatalf("buffer should be intact: %d files remain, want 2", len(files))
	}
}

func TestFlushRecordsLastDeliveryError(t *testing.T) {
	isolateConfigCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 1)

	_, err := Flush(s, srv.URL, "", nil, 2*time.Second)
	if err == nil {
		t.Fatal("want error on server 500")
	}
	got, ok := readLastDeliveryError(s)
	if !ok {
		t.Fatal("want last delivery error recorded")
	}
	if !strings.Contains(got, err.Error()) {
		t.Fatalf("last delivery error = %q, want it to contain %q", got, err.Error())
	}
}

func TestFlushClearsLastDeliveryErrorAfterSuccess(t *testing.T) {
	isolateConfigCache(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := &Outbox{Dir: t.TempDir()}
	if err := os.WriteFile(lastDeliveryErrorPath(s), []byte("old failure"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed(t, s, 1)

	if _, err := Flush(s, srv.URL, "", nil, 2*time.Second); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got, ok := readLastDeliveryError(s); ok {
		t.Fatalf("last delivery error should be cleared after success, got %q", got)
	}
}

func TestFlushEmptyEndpointIsNoop(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 1)
	sent, err := Flush(s, "", "", nil, time.Second)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sent != 0 {
		t.Fatalf("sent = %d, want 0", sent)
	}
	files, _ := s.List()
	if len(files) != 1 {
		t.Fatalf("buffer changed: %d files", len(files))
	}
}

func TestFlushSkipsWhenLocked(t *testing.T) {
	s := &Outbox{Dir: t.TempDir()}
	seed(t, s, 1)
	// Hold the lock from this test.
	release, err := lockOutbox(s)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	defer release()

	sent, err := Flush(s, "http://127.0.0.1:0", "", nil, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if sent != 0 {
		t.Fatalf("sent = %d, want 0 (should skip when locked)", sent)
	}
	files, _ := s.List()
	if len(files) != 1 {
		t.Fatalf("buffer changed while locked: %d files", len(files))
	}
}
