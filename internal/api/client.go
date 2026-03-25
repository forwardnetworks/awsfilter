package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	baseURL    *url.URL
	apiPrefix  string
	username   string
	password   string
	httpClient *http.Client
}

type SnapshotInfo struct {
	ID          string `json:"id"`
	CreatedAt   string `json:"createdAt,omitempty"`
	State       string `json:"state,omitempty"`
	ProcessedAt string `json:"processedAt,omitempty"`
	Note        string `json:"note,omitempty"`
}

type NetworkSnapshots struct {
	Snapshots []SnapshotInfo `json:"snapshots"`
}

func NewClient(host, apiPrefix, username, password string, insecure bool, timeout time.Duration) (*Client, error) {
	baseURL, err := normalizeHost(host)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(username) == "" {
		return nil, fmt.Errorf("username is required")
	}
	if strings.TrimSpace(password) == "" {
		return nil, fmt.Errorf("password is required")
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.InsecureSkipVerify = insecure //nolint:gosec

	return &Client{
		baseURL:   baseURL,
		apiPrefix: normalizeAPIPrefix(apiPrefix),
		username:  username,
		password:  password,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}, nil
}

func (c *Client) LatestProcessedSnapshotID(ctx context.Context, networkID string) (string, error) {
	snapshot, err := c.LatestProcessedSnapshot(ctx, networkID)
	if err != nil {
		return "", err
	}
	return snapshot.ID, nil
}

func (c *Client) LatestProcessedSnapshot(ctx context.Context, networkID string) (*SnapshotInfo, error) {
	if strings.TrimSpace(networkID) == "" {
		return nil, fmt.Errorf("network ID is required")
	}
	body, status, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/networks/%s/snapshots/latestProcessed", networkID), "application/json")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("latest snapshot request failed with status %d: %s", status, strings.TrimSpace(string(body)))
	}
	var snapshot SnapshotInfo
	if err := json.Unmarshal(body, &snapshot); err != nil {
		return nil, fmt.Errorf("decode latest snapshot response: %w", err)
	}
	if snapshot.ID == "" {
		return nil, fmt.Errorf("latest snapshot response did not include an id")
	}
	return &snapshot, nil
}

func (c *Client) ListSnapshots(ctx context.Context, networkID string) ([]SnapshotInfo, error) {
	if strings.TrimSpace(networkID) == "" {
		return nil, fmt.Errorf("network ID is required")
	}
	body, status, err := c.do(
		ctx,
		http.MethodGet,
		fmt.Sprintf("/networks/%s/snapshots?includeArchived=true", networkID),
		"application/json",
	)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("list snapshots request failed with status %d: %s", status, strings.TrimSpace(string(body)))
	}
	var snapshots NetworkSnapshots
	if err := json.Unmarshal(body, &snapshots); err != nil {
		return nil, fmt.Errorf("decode snapshots response: %w", err)
	}
	return snapshots.Snapshots, nil
}

func (c *Client) DownloadSnapshot(ctx context.Context, snapshotID string) ([]byte, error) {
	if strings.TrimSpace(snapshotID) == "" {
		return nil, fmt.Errorf("snapshot ID is required")
	}
	body, status, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/snapshots/%s", snapshotID), "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("snapshot export request failed with status %d: %s", status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c *Client) ImportSnapshot(ctx context.Context, networkID, archivePath, note string) (string, error) {
	if strings.TrimSpace(networkID) == "" {
		return "", fmt.Errorf("network ID is required")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open archive for import: %w", err)
	}
	defer file.Close()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(archivePath))
	if err != nil {
		return "", fmt.Errorf("create multipart file field: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return "", fmt.Errorf("copy archive into multipart body: %w", err)
	}
	if strings.TrimSpace(note) != "" {
		if err := writer.WriteField("note", note); err != nil {
			return "", fmt.Errorf("add note field: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("finalize multipart body: %w", err)
	}

	endpoint, err := c.resolve(fmt.Sprintf("/networks/%s/snapshots", networkID))
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), &body)
	if err != nil {
		return "", fmt.Errorf("build import request: %w", err)
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("perform import request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read import response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("snapshot import request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	var snapshot SnapshotInfo
	if err := json.Unmarshal(respBody, &snapshot); err != nil {
		return "", fmt.Errorf("decode import snapshot response: %w", err)
	}
	if snapshot.ID == "" {
		return "", fmt.Errorf("snapshot import response did not include an id")
	}
	return snapshot.ID, nil
}

func (c *Client) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	if strings.TrimSpace(snapshotID) == "" {
		return fmt.Errorf("snapshot ID is required")
	}
	body, status, err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/snapshots/%s", snapshotID), "application/json")
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("delete snapshot request failed with status %d: %s", status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, endpointPath, accept string) ([]byte, int, error) {
	endpoint, err := c.resolve(endpointPath)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.SetBasicAuth(c.username, c.password)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read response body: %w", err)
	}
	return body, resp.StatusCode, nil
}

func (c *Client) resolve(endpointPath string) (*url.URL, error) {
	relative := endpointPath
	if !strings.HasPrefix(relative, "/") {
		relative = "/" + relative
	}
	if c.apiPrefix != "" && !strings.HasPrefix(relative, c.apiPrefix+"/") && relative != c.apiPrefix {
		relative = c.apiPrefix + relative
	}
	relativeURL, err := url.Parse(relative)
	if err != nil {
		return nil, fmt.Errorf("parse endpoint path: %w", err)
	}
	return c.baseURL.ResolveReference(relativeURL), nil
}

func normalizeHost(host string) (*url.URL, error) {
	value := strings.TrimSpace(host)
	if value == "" {
		return nil, fmt.Errorf("host is required")
	}
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return nil, fmt.Errorf("invalid host: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("host must include a scheme and hostname")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

func normalizeAPIPrefix(prefix string) string {
	value := strings.TrimSpace(prefix)
	if value == "" || value == "/" {
		return ""
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	value = path.Clean(value)
	if value == "." || value == "/" {
		return ""
	}
	return strings.TrimRight(value, "/")
}
