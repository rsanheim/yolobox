package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClipboardBridgeCopyAndPasteHandlers(t *testing.T) {
	oldWrite := writeHostClipboard
	oldRead := readHostClipboard
	defer func() {
		writeHostClipboard = oldWrite
		readHostClipboard = oldRead
	}()

	var copied string
	writeHostClipboard = func(text string) error {
		copied = text
		return nil
	}
	readHostClipboard = func() (string, error) {
		return copied, nil
	}

	bridge := &clipboardBridge{Token: "secret"}

	copyReq := httptest.NewRequest(http.MethodPost, "/copy", strings.NewReader("hello from container"))
	copyReq.Header.Set("X-Yolobox-Clipboard-Token", "secret")
	copyRec := httptest.NewRecorder()
	bridge.handleCopy(copyRec, copyReq)
	if copyRec.Code != http.StatusNoContent {
		t.Fatalf("expected copy status 204, got %d: %s", copyRec.Code, copyRec.Body.String())
	}
	if copied != "hello from container" {
		t.Fatalf("unexpected copied text %q", copied)
	}

	pasteReq := httptest.NewRequest(http.MethodGet, "/paste", nil)
	pasteReq.Header.Set("X-Yolobox-Clipboard-Token", "secret")
	pasteRec := httptest.NewRecorder()
	bridge.handlePaste(pasteRec, pasteReq)
	if pasteRec.Code != http.StatusOK {
		t.Fatalf("expected paste status 200, got %d: %s", pasteRec.Code, pasteRec.Body.String())
	}
	if pasteRec.Body.String() != "hello from container" {
		t.Fatalf("unexpected pasted text %q", pasteRec.Body.String())
	}
}

func TestClipboardBridgeRejectsBadToken(t *testing.T) {
	bridge := &clipboardBridge{Token: "secret"}
	req := httptest.NewRequest(http.MethodPost, "/copy", strings.NewReader("nope"))
	req.Header.Set("X-Yolobox-Clipboard-Token", "wrong")
	rec := httptest.NewRecorder()

	bridge.handleCopy(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", rec.Code)
	}
}
