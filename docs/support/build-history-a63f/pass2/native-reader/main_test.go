package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const ref = "imhco5xoke5vkderq2l42eltr"

func msg(n int, b []byte) []byte { return messageField(n, b) }
func entry(k, v string) []byte   { return append(msg(1, []byte(k)), msg(2, []byte(v))...) }
func event(r string) []byte {
	b := msg(1, []byte(r))
	b = append(b, msg(7, []byte{8, 128, 200, 175, 216, 6})...)
	b = append(b, msg(9, entry("containerimage.config.digest", "sha256:"+strings.Repeat("a", 64)))...)
	b = append(b, msg(9, entry("containerimage.digest", "sha256:"+strings.Repeat("b", 64)))...)
	return append([]byte{8, 1}, msg(2, b)...)
}
func response(payload []byte, status string) *http.Response {
	b := make([]byte, 5)
	binary.BigEndian.PutUint32(b[1:], uint32(len(payload)))
	b = append(b, payload...)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(b)), Trailer: http.Header{"Grpc-Status": {status}}}
}
func TestExactRequest(t *testing.T) {
	b, e := request(ref)
	if e != nil {
		t.Fatal(e)
	}
	f, e := fields(b[5:])
	if e != nil || len(f) != 3 || f[0].number != 2 || string(f[0].data) != ref || f[1].number != 3 || f[1].integer != 1 || f[2].number != 5 || f[2].integer != 1 {
		t.Fatalf("unexpected request %v %v", f, e)
	}
}
func TestRefRequired(t *testing.T) {
	for _, v := range []string{"", "latest", "../x", ref + "/../"} {
		if _, e := request(v); e == nil {
			t.Fatal(v)
		}
	}
}
func TestDistinctDigests(t *testing.T) {
	p, e := project(event(ref), ref)
	if e != nil {
		t.Fatal(e)
	}
	m := p["exporter_response"].(map[string]string)
	if m["containerimage.config.digest"] == m["containerimage.digest"] {
		t.Fatal("digest conflation")
	}
}
func TestForeignRef(t *testing.T) {
	if _, e := project(event(strings.Repeat("a", 24)), ref); e == nil {
		t.Fatal("foreign ref")
	}
}
func TestNoRecord(t *testing.T) {
	if _, e := project(nil, ref); e == nil {
		t.Fatal("missing record")
	}
}
func TestPrivateFieldsNotProjected(t *testing.T) {
	f, _ := fields(event(ref))
	r := f[1].data
	r = append(r, msg(3, entry("build-arg:SECRET", "private sentinel"))...)
	r = append(r, msg(8, []byte("source logs private sentinel"))...)
	p, e := project(append([]byte{8, 1}, msg(2, r)...), ref)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(p)
	if bytes.Contains(b, []byte("private sentinel")) {
		t.Fatal(string(b))
	}
}
func TestErrorPresenceOnly(t *testing.T) {
	f, _ := fields(event(ref))
	r := append(f[1].data, msg(5, msg(2, []byte("private error sentinel")))...)
	r = append(r, msg(18, []byte("private attachment"))...)
	p, e := project(msg(2, r), ref)
	if e != nil {
		t.Fatal(e)
	}
	if !p["error_present"].(bool) || !p["external_error_present"].(bool) {
		t.Fatal(p)
	}
	b, _ := json.Marshal(p)
	if bytes.Contains(b, []byte("private")) {
		t.Fatal(string(b))
	}
}
func TestMalformed(t *testing.T) {
	for _, b := range [][]byte{{0}, {18, 255}, {18, 2, 10}, {255, 255, 255}} {
		if _, e := project(b, ref); e == nil {
			t.Fatal(b)
		}
	}
}
func TestFrameSuccess(t *testing.T) {
	if _, e := readResult(response(event(ref), "0"), ref); e != nil {
		t.Fatal(e)
	}
}
func TestFrameStatus(t *testing.T) {
	if _, e := readResult(response(event(ref), "13"), ref); e == nil {
		t.Fatal("nonzero status")
	}
}
func TestFrameBound(t *testing.T) {
	r := response(event(ref), "0")
	b := make([]byte, 5)
	binary.BigEndian.PutUint32(b[1:], maxMessage+1)
	r.Body = io.NopCloser(bytes.NewReader(b))
	if _, e := readResult(r, ref); e == nil {
		t.Fatal("oversize")
	}
}
func TestExtraRecord(t *testing.T) {
	r := response(event(ref), "0")
	b, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(append(b, b...)))
	if _, e := readResult(r, ref); e == nil {
		t.Fatal("extra record")
	}
}
func fixtureOutput() (map[string]any, []byte) {
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:` + strings.Repeat("c", 64) + `","size":100},"layers":[],"annotations":{"secret":"not projected"}}`)
	out := map[string]any{"event_type": uint64(1), "error_present": false, "external_error_present": false, "completed_at": "2026-11-25T21:22:26Z", "exporters": []map[string]string{{"type": "image", "name": "docker.io/library/own_service:built"}}, "result_descriptors": []map[string]any{{"media_type": "application/vnd.oci.image.manifest.v1+json", "digest": "sha256:" + strings.Repeat("b", 64), "size": uint64(len(manifest))}}}
	return out, manifest
}
func TestResolveConfigNoConfigRead(t *testing.T) {
	out, raw := fixtureOutput()
	count := 0
	e := resolveConfig(out, "own_service:built", func(d descriptor) ([]byte, error) {
		count++
		if !isManifest(d.MediaType) {
			t.Fatal("config requested")
		}
		return raw, nil
	})
	if e != nil || count != 1 || out["verified_output_config_id"] != "sha256:"+strings.Repeat("c", 64) {
		t.Fatal(out, e, count)
	}
	b, _ := json.Marshal(out)
	if bytes.Contains(b, []byte("not projected")) {
		t.Fatal(string(b))
	}
}
func TestResolveDeniesBeforeContent(t *testing.T) {
	for _, kind := range []string{"failed", "external", "tag", "incomplete", "planless", "running"} {
		out, _ := fixtureOutput()
		tag := "own_service:built"
		switch kind {
		case "failed":
			out["error_present"] = true
		case "external":
			out["external_error_present"] = true
		case "tag":
			tag = "sibling_service:built"
		case "incomplete":
			delete(out, "completed_at")
		case "planless":
			delete(out, "result_descriptors")
		case "running":
			out["event_type"] = uint64(0)
		}
		if e := resolveConfig(out, tag, func(descriptor) ([]byte, error) { t.Fatal("content fetched", kind); return nil, nil }); e == nil {
			t.Fatal(kind)
		}
	}
}
func TestContentDigestValidation(t *testing.T) {
	raw := []byte("{}")
	d := descriptor{Digest: "sha256:" + strings.Repeat("a", 64), Size: 2}
	b := msg(2, raw)
	resp := response(b, "0")
	if _, e := readContent(resp, d); e == nil {
		t.Fatal("wrong digest accepted")
	}
}
func TestOnlyDescriptorProjection(t *testing.T) {
	raw := append(msg(1, []byte("application/vnd.oci.image.index.v1+json")), msg(2, []byte("sha256:"+strings.Repeat("a", 64)))...)
	raw = append(raw, 24, 100)
	raw = append(raw, msg(5, entry("private", "sentinel"))...)
	d, e := descriptors(msg(1, raw))
	if e != nil || len(d) != 1 {
		t.Fatal(d, e)
	}
	b, _ := json.Marshal(d)
	if bytes.Contains(b, []byte("sentinel")) {
		t.Fatal(string(b))
	}
}
