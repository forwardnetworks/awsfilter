package filter

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

type Summary struct {
	CollectedAccountIDs []string      `json:"collected_account_ids"`
	OriginalInterfaces  int           `json:"original_interfaces"`
	KeptInterfaces      int           `json:"kept_interfaces"`
	RemovedInterfaces   int           `json:"removed_interfaces"`
	Files               []FileSummary `json:"files"`
}

type FileSummary struct {
	Name               string `json:"name"`
	OriginalInterfaces int    `json:"original_interfaces"`
	KeptInterfaces     int    `json:"kept_interfaces"`
	RemovedInterfaces  int    `json:"removed_interfaces"`
}

type cloudAccount struct {
	AccountID string `json:"accountId"`
	Enabled   bool   `json:"enabled"`
}

func FilterSnapshotZip(input []byte) ([]byte, Summary, error) {
	reader, err := zip.NewReader(bytes.NewReader(input), int64(len(input)))
	if err != nil {
		return nil, Summary{}, fmt.Errorf("open snapshot zip: %w", err)
	}

	accounts, err := collectedAccounts(reader)
	if err != nil {
		return nil, Summary{}, err
	}
	if len(accounts) == 0 {
		return nil, Summary{}, fmt.Errorf("no enabled AWS collected accounts were found in the snapshot package")
	}
	accountSet := make(map[string]struct{}, len(accounts))
	for _, account := range accounts {
		accountSet[account] = struct{}{}
	}

	var summary Summary
	summary.CollectedAccountIDs = append(summary.CollectedAccountIDs, accounts...)

	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		data, err := readZipFile(file)
		if err != nil {
			return nil, Summary{}, err
		}
		outData := data
		if isCloudInterfacesFile(file.Name) {
			filtered, fileSummary, err := filterInterfacesFile(data, accountSet)
			if err != nil {
				return nil, Summary{}, fmt.Errorf("filter %s: %w", file.Name, err)
			}
			outData = filtered
			summary.OriginalInterfaces += fileSummary.OriginalInterfaces
			summary.KeptInterfaces += fileSummary.KeptInterfaces
			summary.RemovedInterfaces += fileSummary.RemovedInterfaces
			summary.Files = append(summary.Files, fileSummary)
		}
		header := file.FileHeader
		writerEntry, err := writer.CreateHeader(&header)
		if err != nil {
			return nil, Summary{}, fmt.Errorf("create zip entry %s: %w", file.Name, err)
		}
		if _, err := writerEntry.Write(outData); err != nil {
			return nil, Summary{}, fmt.Errorf("write zip entry %s: %w", file.Name, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, Summary{}, fmt.Errorf("finalize output zip: %w", err)
	}

	return out.Bytes(), summary, nil
}

func collectedAccounts(reader *zip.Reader) ([]string, error) {
	accountSet := map[string]struct{}{}
	for _, file := range reader.File {
		if !isCloudAccountsFile(file.Name) {
			continue
		}
		data, err := readZipFile(file)
		if err != nil {
			return nil, err
		}
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[]")) {
			continue
		}
		var accounts []cloudAccount
		if err := json.Unmarshal(trimmed, &accounts); err != nil {
			return nil, fmt.Errorf("decode %s: %w", file.Name, err)
		}
		for _, account := range accounts {
			if account.Enabled && strings.TrimSpace(account.AccountID) != "" {
				accountSet[account.AccountID] = struct{}{}
			}
		}
	}
	accounts := make([]string, 0, len(accountSet))
	for account := range accountSet {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)
	return accounts, nil
}

func filterInterfacesFile(data []byte, accountSet map[string]struct{}) ([]byte, FileSummary, error) {
	var chunks []map[string]any
	if err := json.Unmarshal(data, &chunks); err != nil {
		return nil, FileSummary{}, err
	}

	var summary FileSummary
	for _, chunk := range chunks {
		result, ok := chunk["result"].(map[string]any)
		if !ok {
			continue
		}
		interfaces, ok := result["networkInterfaces"].([]any)
		if !ok {
			continue
		}
		filtered := make([]any, 0, len(interfaces))
		for _, item := range interfaces {
			iface, ok := item.(map[string]any)
			if !ok {
				continue
			}
			summary.OriginalInterfaces++
			ownerID, _ := iface["ownerId"].(string)
			if _, ok := accountSet[ownerID]; ok {
				filtered = append(filtered, iface)
				summary.KeptInterfaces++
			}
		}
		result["networkInterfaces"] = filtered
	}
	summary.RemovedInterfaces = summary.OriginalInterfaces - summary.KeptInterfaces
	encoded, err := json.MarshalIndent(chunks, "", "  ")
	if err != nil {
		return nil, FileSummary{}, err
	}
	return encoded, summary, nil
}

func isCloudAccountsFile(name string) bool {
	base := filepath.Base(name)
	return strings.HasPrefix(base, "collect_aws_") && strings.HasSuffix(base, ",cloud_accounts.json")
}

func isCloudInterfacesFile(name string) bool {
	base := filepath.Base(name)
	return strings.HasPrefix(base, "collect_aws_") && strings.HasSuffix(base, ",cloud_interfaces_json.json")
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", file.Name, err)
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", file.Name, err)
	}
	return data, nil
}
