// Support-only read-only diagnostic. Not installed or exposed to task agents.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"time"

	"golang.org/x/net/http2"
)

const rpc = "/moby.buildkit.v1.Control/ListenBuildHistory"
const maxMessage = 1 << 20

var validRef = regexp.MustCompile(`^[a-z0-9]{20,40}$`)
var digest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

type field struct {
	number  int
	integer uint64
	data    []byte
}

func fields(b []byte) ([]field, error) {
	var out []field
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 || key>>3 == 0 {
			return nil, errors.New("invalid protobuf")
		}
		b = b[n:]
		f := field{number: int(key >> 3)}
		switch key & 7 {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return nil, errors.New("invalid integer")
			}
			f.integer = v
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return nil, io.ErrUnexpectedEOF
			}
			b = b[8:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || l > uint64(len(b)-n) {
				return nil, io.ErrUnexpectedEOF
			}
			b = b[n:]
			f.data = b[:int(l)]
			b = b[int(l):]
		case 5:
			if len(b) < 4 {
				return nil, io.ErrUnexpectedEOF
			}
			b = b[4:]
		default:
			return nil, errors.New("unsupported wire type")
		}
		out = append(out, f)
		if len(out) > 32768 {
			return nil, errors.New("field limit")
		}
	}
	return out, nil
}
func stringField(b []byte, n int) (string, error) {
	fs, e := fields(b)
	if e != nil {
		return "", e
	}
	var s string
	for _, f := range fs {
		if f.number == n {
			s = string(f.data)
		}
	}
	return s, nil
}
func timestamp(b []byte) (string, error) {
	fs, e := fields(b)
	if e != nil {
		return "", e
	}
	var s, ns int64
	for _, f := range fs {
		if f.number == 1 {
			s = int64(f.integer)
		}
		if f.number == 2 {
			ns = int64(f.integer)
		}
	}
	if s <= 0 || ns >= 1e9 {
		return "", errors.New("invalid timestamp")
	}
	return time.Unix(s, ns).UTC().Format(time.RFC3339Nano), nil
}
func mapEntry(b []byte) (string, string, error) {
	k, e := stringField(b, 1)
	if e != nil {
		return "", "", e
	}
	v, e := stringField(b, 2)
	return k, v, e
}
func messageField(n int, b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(n<<3|2))
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}
func request(ref string) ([]byte, error) {
	if !validRef.MatchString(ref) {
		return nil, errors.New("exact native ref required")
	}
	// Ref=2; EarlyExit=3; Limit=5. No active subscription, latest selection or mutation.
	b := messageField(2, []byte(ref))
	b = append(b, 24, 1, 40, 1)
	frame := make([]byte, 5)
	binary.BigEndian.PutUint32(frame[1:], uint32(len(b)))
	return append(frame, b...), nil
}
func descriptors(b []byte) ([]map[string]any, error) {
	fs, e := fields(b)
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	for _, f := range fs {
		var raw []byte
		if f.number == 1 {
			raw = f.data
		}
		if f.number == 3 {
			entries, err := fields(f.data)
			if err != nil {
				return nil, err
			}
			for _, v := range entries {
				if v.number == 2 {
					raw = v.data
				}
			}
		}
		if raw == nil {
			continue
		}
		df, err := fields(raw)
		if err != nil {
			return nil, err
		}
		d := map[string]any{}
		for _, v := range df {
			if v.number == 1 {
				d["media_type"] = string(v.data)
			}
			if v.number == 2 {
				if !digest.Match(v.data) {
					return nil, errors.New("invalid result digest")
				}
				d["digest"] = string(v.data)
			}
			if v.number == 3 {
				d["size"] = v.integer
			}
		}
		out = append(out, d)
	}
	return out, nil
}
func project(event []byte, expected string) (map[string]any, error) {
	fs, e := fields(event)
	if e != nil {
		return nil, e
	}
	var record []byte
	var eventType uint64
	for _, f := range fs {
		if f.number == 1 {
			eventType = f.integer
		}
		if f.number == 2 {
			record = f.data
		}
	}
	if len(record) == 0 {
		return nil, errors.New("missing record")
	}
	fs, e = fields(record)
	if e != nil {
		return nil, e
	}
	out := map[string]any{"ref": expected, "event_type": eventType, "error_present": false, "external_error_present": false, "exporters": []map[string]string{}, "exporter_response": map[string]string{}}
	var actual string
	for _, f := range fs {
		switch f.number {
		case 1:
			actual = string(f.data)
		case 4:
			ef, err := fields(f.data)
			if err != nil {
				return nil, err
			}
			ex := map[string]string{}
			for _, v := range ef {
				if v.number == 1 {
					ex["type"] = string(v.data)
				}
				if v.number == 2 {
					k, val, err := mapEntry(v.data)
					if err != nil {
						return nil, err
					}
					if k == "name" {
						ex[k] = val
					}
				}
			}
			out["exporters"] = append(out["exporters"].([]map[string]string), ex)
		case 5:
			out["error_present"] = true // Never decode error details/source.
		case 6, 7:
			t, err := timestamp(f.data)
			if err != nil {
				return nil, err
			}
			if f.number == 6 {
				out["created_at"] = t
			} else {
				out["completed_at"] = t
			}
		case 9:
			k, v, err := mapEntry(f.data)
			if err != nil {
				return nil, err
			}
			if k == "image.name" || k == "containerimage.config.digest" || k == "containerimage.digest" {
				if k != "image.name" && !digest.MatchString(v) {
					return nil, errors.New("invalid digest")
				}
				out["exporter_response"].(map[string]string)[k] = v
			}
		case 10:
			d, err := descriptors(f.data)
			if err != nil {
				return nil, err
			}
			out["result_descriptors"] = d
		case 11:
			ef, err := fields(f.data)
			if err != nil {
				return nil, err
			}
			for _, v := range ef {
				if v.number == 2 {
					d, err := descriptors(v.data)
					if err != nil {
						return nil, err
					}
					existing, _ := out["platform_result_descriptors"].([]map[string]any)
					out["platform_result_descriptors"] = append(existing, d...)
				}
			}
		case 18:
			out["external_error_present"] = true
		}
	}
	if actual != expected {
		return nil, errors.New("native ref mismatch")
	}
	return out, nil
}

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c bufferedConn) Read(b []byte) (int, error) { return c.r.Read(b) }
func engineDial(ctx context.Context, _ string, _ string, _ *tls.Config) (net.Conn, error) {
	c, e := (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
	if e != nil {
		return nil, e
	}
	_ = c.SetDeadline(time.Now().Add(20 * time.Second))
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://docker/grpc", http.NoBody)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "h2c")
	if e = req.Write(c); e != nil {
		c.Close()
		return nil, e
	}
	r := bufio.NewReader(c)
	resp, e := http.ReadResponse(r, req)
	if e != nil {
		c.Close()
		return nil, e
	}
	if resp.StatusCode != 101 {
		c.Close()
		return nil, fmt.Errorf("engine upgrade HTTP %d", resp.StatusCode)
	}
	return bufferedConn{c, r}, nil
}
func readResult(resp *http.Response, ref string) (map[string]any, error) {
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("RPC HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxMessage+6)
	var header [5]byte
	_, e := io.ReadFull(limited, header[:])
	if e == io.EOF {
		return nil, fmt.Errorf("native record unavailable (grpc-status %s)", resp.Trailer.Get("Grpc-Status"))
	}
	if e != nil {
		return nil, e
	}
	n := binary.BigEndian.Uint32(header[1:])
	if header[0] != 0 || n > maxMessage {
		return nil, errors.New("unsupported or oversized frame")
	}
	b := make([]byte, n)
	if _, e = io.ReadFull(limited, b); e != nil {
		return nil, e
	}
	var extra [1]byte
	if _, e = limited.Read(extra[:]); e != io.EOF {
		return nil, errors.New("multiple records or incomplete RPC")
	}
	if resp.Trailer.Get("Grpc-Status") != "0" {
		return nil, errors.New("RPC did not complete successfully")
	}
	return project(b, ref)
}

// Only index/manifest JSON linked from the exact completed history record is read.
// Config blobs, layers, attestations, logs and provenance are never requested.
type descriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
}

func grpcRequest(ctx context.Context, tr *http2.Transport, path string, b []byte) (*http.Response, error) {
	frame := make([]byte, 5)
	binary.BigEndian.PutUint32(frame[1:], uint32(len(b)))
	frame = append(frame, b...)
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://docker"+path, bytes.NewReader(frame))
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Te", "trailers")
	req.Header.Set("Grpc-Timeout", "20S")
	return tr.RoundTrip(req)
}
func readContent(resp *http.Response, expected descriptor) ([]byte, error) {
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New("content RPC HTTP failure")
	}
	r := io.LimitReader(resp.Body, 131072)
	var data []byte
	for frames := 0; ; frames++ {
		var h [5]byte
		_, e := io.ReadFull(r, h[:])
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		n := binary.BigEndian.Uint32(h[1:])
		if frames >= 8 || h[0] != 0 || n > 65550 {
			return nil, errors.New("content frame limit")
		}
		b := make([]byte, n)
		if _, e = io.ReadFull(r, b); e != nil {
			return nil, e
		}
		fs, e := fields(b)
		if e != nil {
			return nil, e
		}
		var offset uint64
		var chunk []byte
		for _, f := range fs {
			if f.number == 1 {
				offset = f.integer
			}
			if f.number == 2 {
				chunk = f.data
			}
		}
		if offset != uint64(len(data)) || len(data)+len(chunk) > 65536 {
			return nil, errors.New("content size or offset mismatch")
		}
		data = append(data, chunk...)
	}
	if resp.Trailer.Get("Grpc-Status") != "0" || len(data) != int(expected.Size) || fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != expected.Digest {
		return nil, errors.New("content identity or RPC failure")
	}
	return data, nil
}
func isIndex(t string) bool {
	return t == "application/vnd.oci.image.index.v1+json" || t == "application/vnd.docker.distribution.manifest.list.v2+json"
}
func isManifest(t string) bool {
	return t == "application/vnd.oci.image.manifest.v1+json" || t == "application/vnd.docker.distribution.manifest.v2+json"
}
func resolveConfig(out map[string]any, tag string, fetch func(descriptor) ([]byte, error)) error {
	if out["event_type"] != uint64(1) || out["error_present"] != false || out["external_error_present"] != false || out["completed_at"] == nil {
		return errors.New("not successful completion")
	}
	ex, _ := out["exporters"].([]map[string]string)
	if len(ex) != 1 || ex[0]["type"] != "image" || (ex[0]["name"] != tag && ex[0]["name"] != "docker.io/library/"+tag) {
		return errors.New("output tag mismatch")
	}
	var d descriptor
	results, ok := out["result_descriptors"].([]map[string]any)
	if !ok || len(results) == 0 {
		return errors.New("missing output descriptors")
	}
	for _, v := range results {
		candidate := descriptor{MediaType: v["media_type"].(string), Digest: v["digest"].(string), Size: int64(v["size"].(uint64))}
		if d.Digest != "" && d.Digest != candidate.Digest {
			return errors.New("multiple outputs")
		}
		d = candidate
	}
	chain := []descriptor{}
	for depth := 0; depth < 2; depth++ {
		if !digest.MatchString(d.Digest) || d.Size <= 0 || d.Size > 65536 || (!isIndex(d.MediaType) && !isManifest(d.MediaType)) {
			return errors.New("not an admitted output manifest")
		}
		raw, e := fetch(d)
		if e != nil {
			return e
		}
		chain = append(chain, d)
		var doc struct {
			SchemaVersion int          `json:"schemaVersion"`
			MediaType     string       `json:"mediaType"`
			Manifests     []descriptor `json:"manifests"`
			Config        descriptor   `json:"config"`
		}
		if e = json.Unmarshal(raw, &doc); e != nil {
			return e
		}
		if doc.SchemaVersion != 2 || doc.MediaType != d.MediaType {
			return errors.New("manifest schema mismatch")
		}
		if isIndex(d.MediaType) {
			var selected []descriptor
			for _, v := range doc.Manifests {
				if isManifest(v.MediaType) && v.Platform.OS == "linux" && v.Platform.Architecture == "amd64" {
					selected = append(selected, v)
				}
			}
			if len(selected) != 1 {
				return errors.New("ambiguous native platform")
			}
			d = selected[0]
			continue
		}
		if (doc.Config.MediaType != "application/vnd.oci.image.config.v1+json" && doc.Config.MediaType != "application/vnd.docker.container.image.v1+json") || !digest.MatchString(doc.Config.Digest) {
			return errors.New("invalid config descriptor")
		}
		out["verified_output_config_id"] = doc.Config.Digest
		out["output_manifest_chain"] = chain
		return nil
	}
	return errors.New("manifest depth limit")
}
func main() {
	if len(os.Args) != 2 && len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "one exact native ref required")
		os.Exit(2)
	}
	ref := os.Args[1]
	body, e := request(ref)
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tr := &http2.Transport{AllowHTTP: true, DialTLSContext: engineDial}
	defer tr.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://docker"+rpc, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Te", "trailers")
	req.Header.Set("Grpc-Timeout", "20S")
	resp, e := tr.RoundTrip(req)
	if e == nil {
		var out map[string]any
		out, e = readResult(resp, ref)
		if e == nil {
			if len(os.Args) == 3 {
				e = resolveConfig(out, os.Args[2], func(d descriptor) ([]byte, error) {
					req := messageField(1, []byte(d.Digest))
					req = append(req, 24)
					req = binary.AppendUvarint(req, uint64(d.Size))
					resp, err := grpcRequest(ctx, tr, "/containerd.services.content.v1.Content/Read", req)
					if err != nil {
						return nil, err
					}
					return readContent(resp, d)
				})
			}
			if e == nil {
				e = json.NewEncoder(os.Stdout).Encode(out)
			}
		}
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
