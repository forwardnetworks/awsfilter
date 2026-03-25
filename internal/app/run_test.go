package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestRunFiltersToCollectedAccounts(t *testing.T) {
	zipBytes := buildSampleZip(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "alice" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/networks/network-1/snapshots/latestProcessed":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"snapshot-source"}`))
		case "/api/snapshots/snapshot-source":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(zipBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "filtered.zip")
	summary, err := Run(context.Background(), Config{
		Host:      server.URL,
		Username:  "alice",
		Password:  "secret",
		NetworkID: "network-1",
		Output:    output,
		APIPrefix: "/api",
		Insecure:  true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if summary.SnapshotID != "snapshot-source" {
		t.Fatalf("unexpected snapshot id %q", summary.SnapshotID)
	}
	if summary.KeptInterfaces != 2 || summary.RemovedInterfaces != 1 {
		t.Fatalf("unexpected interface counts: %+v", summary)
	}

	filteredBytes, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(filteredBytes), int64(len(filteredBytes)))
	if err != nil {
		t.Fatalf("open output zip: %v", err)
	}
	var found bool
	for _, file := range zr.File {
		if file.Name != "collect_aws_1,cloud_interfaces_json.json" {
			continue
		}
		found = true
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open filtered interfaces file: %v", err)
		}
		defer reader.Close()
		var chunks []map[string]any
		if err := json.NewDecoder(reader).Decode(&chunks); err != nil {
			t.Fatalf("decode filtered interfaces file: %v", err)
		}
		interfaces := chunks[0]["result"].(map[string]any)["networkInterfaces"].([]any)
		if len(interfaces) != 2 {
			t.Fatalf("expected 2 interfaces after filtering, got %d", len(interfaces))
		}
	}
	if !found {
		t.Fatal("filtered interfaces file not found in output zip")
	}
}

func TestRunImportsAndDeletesSourceSnapshot(t *testing.T) {
	zipBytes := buildSampleZip(t)
	var importedBody []byte
	var importedContentType string
	var deletedPath string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "alice" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/networks/network-1/snapshots/latestProcessed":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"snapshot-source"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/networks/network-1/snapshots":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"snapshots":[{"id":"snapshot-imported","state":"PROCESSED"},{"id":"snapshot-source","state":"PROCESSED"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/snapshots/snapshot-source":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(zipBytes)
		case r.Method == http.MethodPost && r.URL.Path == "/api/networks/network-1/snapshots":
			importedContentType = r.Header.Get("Content-Type")
			importedBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"snapshot-imported"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/snapshots/snapshot-source":
			deletedPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	output := filepath.Join(t.TempDir(), "filtered.zip")
	summary, err := Run(context.Background(), Config{
		Host:                 server.URL,
		Username:             "alice",
		Password:             "secret",
		NetworkID:            "network-1",
		Output:               output,
		APIPrefix:            "/api",
		Insecure:             true,
		Import:               true,
		ImportNote:           "test import note",
		DeleteSourceSnapshot: true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if summary.ImportedSnapshotID != "snapshot-imported" {
		t.Fatalf("unexpected imported snapshot id %q", summary.ImportedSnapshotID)
	}
	if !summary.DeletedSourceSnapshot {
		t.Fatalf("expected source snapshot deletion in summary: %+v", summary)
	}
	if deletedPath != "/api/snapshots/snapshot-source" {
		t.Fatalf("unexpected deleted path %q", deletedPath)
	}
	if importedContentType == "" {
		t.Fatal("missing import content type")
	}
	mediaType, params, err := mime.ParseMediaType(importedContentType)
	if err != nil {
		t.Fatalf("parse media type: %v", err)
	}
	if mediaType != "multipart/form-data" {
		t.Fatalf("unexpected media type %q", mediaType)
	}
	reader := multipart.NewReader(bytes.NewReader(importedBody), params["boundary"])
	parts := map[string]string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read multipart part: %v", err)
		}
		body, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read part body: %v", err)
		}
		parts[part.FormName()] = string(body)
	}
	if parts["note"] != "test import note" {
		t.Fatalf("unexpected import note %q", parts["note"])
	}
	interfaces, err := interfacesInZip([]byte(parts["file"]))
	if err != nil {
		t.Fatalf("inspect imported zip: %v", err)
	}
	if len(interfaces) != 2 || interfaces[0] != "eni-1" || interfaces[1] != "eni-2" {
		t.Fatalf("unexpected filtered interfaces in uploaded zip: %#v", interfaces)
	}
}

func buildSampleZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, body string) {
		writer, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("collect_aws_1,cloud_accounts.json", `[
  {"accountId":"account-a","enabled":true},
  {"accountId":"account-b","enabled":true},
  {"accountId":"account-disabled","enabled":false}
]`)
	write("collect_aws_2,cloud_accounts.json", `[]`)
	write("collect_aws_1,cloud_interfaces_json.json", `[
  {
    "collectionAccountId":"account-a",
    "result":{
      "networkInterfaces":[
        {"ownerId":"account-a","networkInterfaceId":"eni-1"},
        {"ownerId":"account-b","networkInterfaceId":"eni-2"},
        {"ownerId":"account-external","networkInterfaceId":"eni-3"}
      ]
    }
  }
]`)
	write("snapshot-package.txt", fmt.Sprintf("sample %d", 1))
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func interfacesInZip(data []byte) ([]string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	for _, file := range reader.File {
		if file.Name != "collect_aws_1,cloud_interfaces_json.json" {
			continue
		}
		rc, err := file.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		var chunks []map[string]any
		if err := json.NewDecoder(rc).Decode(&chunks); err != nil {
			return nil, err
		}
		items := chunks[0]["result"].(map[string]any)["networkInterfaces"].([]any)
		result := make([]string, 0, len(items))
		for _, item := range items {
			result = append(result, item.(map[string]any)["networkInterfaceId"].(string))
		}
		return result, nil
	}
	return nil, fmt.Errorf("interfaces file not found")
}
