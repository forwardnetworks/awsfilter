package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/forwardnetworks/awsfilter/internal/api"
	"github.com/forwardnetworks/awsfilter/internal/filter"
)

type Config struct {
	Host                 string
	Username             string
	Password             string
	NetworkID            string
	Output               string
	APIPrefix            string
	Insecure             bool
	Timeout              time.Duration
	Import               bool
	ImportNote           string
	DeleteSourceSnapshot bool
}

type Summary struct {
	Host                  string               `json:"host"`
	NetworkID             string               `json:"network_id"`
	SnapshotID            string               `json:"snapshot_id"`
	Output                string               `json:"output"`
	CollectedAccountIDs   []string             `json:"collected_account_ids"`
	OriginalInterfaces    int                  `json:"original_interfaces"`
	KeptInterfaces        int                  `json:"kept_interfaces"`
	RemovedInterfaces     int                  `json:"removed_interfaces"`
	Files                 []filter.FileSummary `json:"files"`
	ImportedSnapshotID    string               `json:"imported_snapshot_id,omitempty"`
	DeletedSourceSnapshot bool                 `json:"deleted_source_snapshot"`
}

func Run(ctx context.Context, cfg Config) (*Summary, error) {
	client, err := api.NewClient(cfg.Host, cfg.APIPrefix, cfg.Username, cfg.Password, cfg.Insecure, cfg.Timeout)
	if err != nil {
		return nil, err
	}
	snapshotID, err := client.LatestProcessedSnapshotID(ctx, cfg.NetworkID)
	if err != nil {
		return nil, err
	}
	archive, err := client.DownloadSnapshot(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	filteredArchive, details, err := filter.FilterSnapshotZip(archive)
	if err != nil {
		return nil, err
	}
	outputPath := cfg.Output
	if strings.TrimSpace(outputPath) == "" {
		outputPath = fmt.Sprintf("network-%s-snapshot-%s-collected-compute-only.zip", cfg.NetworkID, snapshotID)
	}
	outputPath, err = filepath.Abs(outputPath)
	if err != nil {
		return nil, fmt.Errorf("resolve output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}
	if err := os.WriteFile(outputPath, filteredArchive, 0o644); err != nil {
		return nil, fmt.Errorf("write output zip: %w", err)
	}

	var importedSnapshotID string
	if cfg.Import {
		importedSnapshotID, err = client.ImportSnapshot(ctx, cfg.NetworkID, outputPath, cfg.ImportNote)
		if err != nil {
			return nil, err
		}
	}
	deletedSource := false
	if cfg.DeleteSourceSnapshot {
		if !cfg.Import {
			return nil, fmt.Errorf("--delete-source-snapshot requires --import")
		}
		if err := waitForImportedSnapshot(ctx, client, cfg.NetworkID, importedSnapshotID, 10*time.Second); err != nil {
			return nil, err
		}
		if err := client.DeleteSnapshot(ctx, snapshotID); err != nil {
			return nil, err
		}
		deletedSource = true
	}

	return &Summary{
		Host:                  cfg.Host,
		NetworkID:             cfg.NetworkID,
		SnapshotID:            snapshotID,
		Output:                outputPath,
		CollectedAccountIDs:   details.CollectedAccountIDs,
		OriginalInterfaces:    details.OriginalInterfaces,
		KeptInterfaces:        details.KeptInterfaces,
		RemovedInterfaces:     details.RemovedInterfaces,
		Files:                 details.Files,
		ImportedSnapshotID:    importedSnapshotID,
		DeletedSourceSnapshot: deletedSource,
	}, nil
}

func waitForImportedSnapshot(
	ctx context.Context,
	client *api.Client,
	networkID string,
	snapshotID string,
	interval time.Duration,
) error {
	if snapshotID == "" {
		return fmt.Errorf("imported snapshot id is empty")
	}
	if interval <= 0 {
		interval = 10 * time.Second
	}
	for {
		snapshots, err := client.ListSnapshots(ctx, networkID)
		if err != nil {
			return err
		}
		for _, snapshot := range snapshots {
			if snapshot.ID != snapshotID {
				continue
			}
			switch snapshot.State {
			case "PROCESSED":
				return nil
			case "FAILED":
				return fmt.Errorf("imported snapshot %s entered FAILED state", snapshotID)
			}
			break
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
