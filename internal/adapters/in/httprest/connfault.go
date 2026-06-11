package httprest

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/mockwave/mockwave/internal/domain/pipeline"
)

// handleConnFault executes a terminal connection-level fault directly on the
// connection. Returns true when it handled the response (the caller must then
// stop). hang/reset require hijacking the raw conn; halfResponse writes a
// partial body and closes.
func handleConnFault(w http.ResponseWriter, pctx *pipeline.PipelineContext) bool {
	switch pctx.ConnFault {
	case "hang":
		// Block up to MaxMs (or until client disconnects), then close silently.
		d := time.Duration(pctx.ConnFaultMaxMs) * time.Millisecond
		<-time.After(d)
		hijackClose(w)
		return true
	case "reset":
		hijackReset(w)
		return true
	case "halfResponse":
		writeHalf(w, pctx)
		return true
	}
	return false
}

// hijackReset sends a TCP RST by setting SO_LINGER 0 then closing.
func hijackReset(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0) // linger 0 → close sends RST
	}
	_ = conn.Close()
}

// hijackClose closes the hijacked connection without writing anything (FIN).
func hijackClose(w http.ResponseWriter) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	_ = conn.Close()
}

// writeHalf writes status + a fraction of the body, then closes abruptly.
func writeHalf(w http.ResponseWriter, pctx *pipeline.PipelineContext) {
	resp := pctx.Response
	if resp == nil {
		hijackClose(w)
		return
	}
	full := bodyBytes(resp.Body)
	n := int(float64(len(full)) * pctx.ConnFaultFraction)
	hj, ok := w.(http.Hijacker)
	if !ok {
		// Fallback: best-effort partial write through the normal writer.
		w.WriteHeader(resp.Status)
		_, _ = w.Write(full[:n])
		return
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	// Minimal HTTP/1.1 response with a Content-Length that promises the full
	// body, but only the prefix is written → client read is truncated.
	_, _ = buf.WriteString("HTTP/1.1 " + strconv.Itoa(resp.Status) + " " + http.StatusText(resp.Status) + "\r\n")
	_, _ = buf.WriteString("Content-Length: " + strconv.Itoa(len(full)) + "\r\n\r\n")
	_, _ = buf.Write(full[:n])
	_ = buf.Flush()
}

// bodyBytes renders a MockResponse body the same way the normal path does.
func bodyBytes(body interface{}) []byte {
	if body == nil {
		return nil
	}
	if s, ok := body.(string); ok {
		return []byte(s)
	}
	b, _ := json.Marshal(body)
	return b
}


// throttledWrite writes b to w at approximately bytesPerSec, flushing chunks.
func throttledWrite(w http.ResponseWriter, b []byte, bytesPerSec int) {
	if bytesPerSec <= 0 {
		_, _ = w.Write(b)
		return
	}
	flusher, _ := w.(http.Flusher)
	const chunk = 256
	interval := time.Duration(float64(time.Second) * float64(chunk) / float64(bytesPerSec))
	for off := 0; off < len(b); off += chunk {
		end := off + chunk
		if end > len(b) {
			end = len(b)
		}
		_, _ = w.Write(b[off:end])
		if flusher != nil {
			flusher.Flush()
		}
		time.Sleep(interval)
	}
}
