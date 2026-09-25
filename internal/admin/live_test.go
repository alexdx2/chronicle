package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/alexdx2/chronicle-core/live"
	"github.com/alexdx2/chronicle-core/store"
)

func dialWS(t *testing.T, ts *httptest.Server, origin string) *websocket.Conn {
	t.Helper()
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", h)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func waitClients(t *testing.T, srv *Server, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for srv.hub.ClientCount() < n {
		if time.Now().After(deadline) {
			t.Fatalf("websocket clients = %d, want %d", srv.hub.ClientCount(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func readLive(t *testing.T, conn *websocket.Conn, within time.Duration) store.LiveEvent {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(within))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("no live message within %v: %v", within, err)
		}
		var msg struct {
			Type string          `json:"type"`
			Data store.LiveEvent `json:"data"`
		}
		if json.Unmarshal(data, &msg) == nil && msg.Type == "live" {
			return msg.Data
		}
	}
}

func diagramTitle(t *testing.T, ev store.LiveEvent) string {
	t.Helper()
	var d live.Diagram
	if err := json.Unmarshal(ev.Payload, &d); err != nil {
		t.Fatal(err)
	}
	return d.Title
}

func TestLiveScreen_StatusCodes(t *testing.T) {
	srv, _ := setupC4Graph(t)
	cases := []struct {
		name, method, ct, origin, body string
		want                           int
	}{
		{"get", "GET", "", "", "", 405},
		{"text/plain", "POST", "text/plain;charset=UTF-8", "", `{"client_id":"a"}`, 415},
		{"foreign origin", "POST", "application/json", "https://evil.example", `{"client_id":"a"}`, 403},
		{"too big", "POST", "application/json", "", `{"client_id":"a","title":"` + strings.Repeat("x", 70<<10) + `"}`, 413},
		{"bad shape", "POST", "application/json", "", `{"client_id":"<x>"}`, 400},
		{"ok", "POST", "application/json", "http://example.com", `{"client_id":"a","tab":"graph","visible":true}`, 204},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "http://example.com/api/live/screen", strings.NewReader(c.body))
		if c.ct != "" {
			req.Header.Set("Content-Type", c.ct)
		}
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		srv.handleLiveScreen(w, req)
		if w.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, w.Code, c.want, w.Body.String())
		}
	}
	evs, _ := srv.getStore().ListLiveEvents(0, live.KindScreen, 10)
	if len(evs) != 1 {
		t.Fatalf("screen events stored = %d, want only the accepted one", len(evs))
	}
}

func TestLive_FeedServesDiagramsOnly(t *testing.T) {
	srv, _ := setupC4Graph(t)
	st := srv.getStore()
	d1, _ := live.PublishDiagram(st, live.SourceMCP, live.Diagram{Title: "one"})
	live.ReportScreen(st, live.Screen{ClientID: "a"})
	live.PublishDiagram(st, live.SourceMCP, live.Diagram{Title: "two"})
	get := func(q string) (int, []store.LiveEvent, http.Header) {
		w := httptest.NewRecorder()
		srv.handleLive(w, httptest.NewRequest("GET", "/api/live"+q, nil))
		var body struct {
			Events []store.LiveEvent `json:"events"`
		}
		json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body.Events, w.Header()
	}
	code, evs, h := get("")
	if code != 200 || len(evs) != 2 || h.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("GET /api/live = %d %d events ACAO=%q", code, len(evs), h.Get("Access-Control-Allow-Origin"))
	}
	if _, evs, _ := get("?after=" + strconv.FormatInt(d1.ID, 10)); len(evs) != 1 || diagramTitle(t, evs[0]) != "two" {
		t.Fatalf("after = %+v", evs)
	}
	for _, q := range []string{"?kind=screen", "?limit=0", "?limit=201", "?after=x"} {
		if code, _, _ := get(q); code != 400 {
			t.Errorf("GET /api/live%s = %d, want 400", q, code)
		}
	}
}

func TestLive_WebSocketCarriesDiagramsNotScreens(t *testing.T) {
	srv, _ := setupC4Graph(t)
	go srv.hub.Run()
	srv.startLiveTail()
	defer srv.stopLiveTail()
	ts := httptest.NewServer(srv)
	defer ts.Close()
	conn := dialWS(t, ts, "")
	waitClients(t, srv, 1)
	live.ReportScreen(srv.getStore(), live.Screen{ClientID: "a"})
	start := time.Now()
	live.PublishDiagram(srv.getStore(), live.SourceMCP, live.Diagram{Title: "fresh"})
	ev := readLive(t, conn, time.Second)
	if ev.Kind != live.KindDiagram || diagramTitle(t, ev) != "fresh" {
		t.Fatalf("first live message = %+v", ev)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("delivered after %v", d)
	}
}

func TestLive_TailFollowsProjectSwitch(t *testing.T) {
	srv, _ := setupC4Graph(t)
	go srv.hub.Run()
	srv.startLiveTail()
	defer srv.stopLiveTail()
	ts := httptest.NewServer(srv)
	defer ts.Close()
	conn := dialWS(t, ts, "")
	waitClients(t, srv, 1)
	other := t.TempDir()
	if err := os.MkdirAll(filepath.Join(other, ".depbot"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := srv.switchTo(other); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if _, err := live.PublishDiagram(srv.getStore(), live.SourceMCP, live.Diagram{Title: "after switch"}); err != nil {
		t.Fatal(err)
	}
	if ev := readLive(t, conn, 2*time.Second); diagramTitle(t, ev) != "after switch" {
		t.Fatalf("event = %+v", ev)
	}
}

func TestWebSocket_OriginCheck(t *testing.T) {
	srv, _ := setupC4Graph(t)
	go srv.hub.Run()
	ts := httptest.NewServer(srv)
	defer ts.Close()
	h := http.Header{}
	h.Set("Origin", "https://evil.example")
	c, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", h)
	if err == nil {
		c.Close()
		t.Fatal("a foreign origin was accepted")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: resp=%v err=%v", resp, err)
	}
	dialWS(t, ts, ts.URL)
	dialWS(t, ts, "")
}
