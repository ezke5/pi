// Package state manages installed package records and ownership checks.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ezke5/pi/internal/footprint"
	"github.com/ezke5/pi/internal/rootfs"
)

type Record struct {
	Pkgname string   `json:"pkgname"`
	Pkgver  string   `json:"pkgver"`
	Pkgrel  string   `json:"pkgrel"`
	Depends []string `json:"depends,omitempty"`
	Backup  []string `json:"backup,omitempty"`
}

func RecordDir(root string) (string, error) {
	return rootfs.TargetPath(root, "/var/lib/pi/packages")
}

func Load(root, name string) (Record, error) {
	dir, err := RecordDir(root)
	if err != nil {
		return Record{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, name+".meta"))
	if err != nil {
		if os.IsNotExist(err) {
			return Record{Pkgname: name}, nil
		}
		return Record{}, fmt.Errorf("read package metadata %q: %w", name, err)
	}
	var record Record
	if err := json.Unmarshal(data, &record); err != nil {
		return Record{}, fmt.Errorf("parse package metadata %q: %w", name, err)
	}
	return record, nil
}

func Installed(root string) ([]Record, error) {
	dir, err := RecordDir(root)
	if err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var records []Record
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".meta") {
			continue
		}
		record, err := Load(root, strings.TrimSuffix(file.Name(), ".meta"))
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Pkgname < records[j].Pkgname })
	return records, nil
}

func Footprint(root, name string) ([]footprint.Entry, error) {
	dir, err := RecordDir(root)
	if err != nil {
		return nil, err
	}
	return footprint.Read(filepath.Join(dir, name+".footprint"))
}

func Write(root string, record Record, entries []footprint.Entry) error {
	dir, err := RecordDir(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create package metadata directory: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	meta := filepath.Join(dir, record.Pkgname+".meta")
	tmp, err := os.CreateTemp(dir, "."+record.Pkgname+".meta-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, meta); err != nil {
		return fmt.Errorf("commit package metadata: %w", err)
	}
	return footprint.Write(filepath.Join(dir, record.Pkgname+".footprint"), entries)
}

func OwnedByOthers(root, name string, path string) (bool, error) {
	records, err := Installed(root)
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if record.Pkgname == name {
			continue
		}
		entries, err := Footprint(root, record.Pkgname)
		if err != nil {
			return false, err
		}
		for _, entry := range entries {
			if entry.Path == path {
				return true, nil
			}
		}
	}
	return false, nil
}
