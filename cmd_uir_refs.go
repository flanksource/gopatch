package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/flanksource/uir/indexer"
	"github.com/flanksource/uir/query"
	"github.com/flanksource/uir/storage"
	"github.com/spf13/pflag"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

type referenceIndexOptions struct {
	DSN    string
	Schema string
}

func addReferenceIndexFlags(flags *pflag.FlagSet, options *referenceIndexOptions) {
	flags.StringVar(&options.DSN, "uir-dsn", "", "Existing UIR index DSN (required for symbol renames and ref).")
	flags.StringVar(&options.Schema, "uir-schema", "", "UIR PostgreSQL schema.")
}

func indexedReferences(ctx context.Context, options referenceIndexOptions, modules []renameModule, refs []typed.SymbolRef, kind typed.Kind) ([]query.ReferenceLocation, error) {
	if options.DSN == "" {
		return nil, errors.New("--uir-dsn is required; index each affected module with uir reindex first")
	}
	database, err := storage.OpenReadOnly(ctx, storage.DBOptions{DSN: options.DSN, Schema: options.Schema})
	if err != nil {
		return nil, err
	}
	sqlDB, err := database.DB()
	if err != nil {
		return nil, err
	}
	defer sqlDB.Close()
	engine, err := indexer.New(database)
	if err != nil {
		return nil, err
	}
	request := query.ReferenceRequest{}
	for _, module := range modules {
		current, err := engine.CheckCurrent(ctx, module.Root, true)
		if err != nil {
			return nil, fmt.Errorf("verify UIR index for %s: %w", module.Root, err)
		}
		request.SnapshotIDs = append(request.SnapshotIDs, current.SnapshotID)
	}
	for _, ref := range refs {
		moduleKey := ""
		for _, module := range modules {
			path := module.Program.ModulePath()
			if (ref.ImportPath == path || strings.HasPrefix(ref.ImportPath, path+"/")) && len(path) > len(moduleKey) {
				moduleKey = path
			}
		}
		request.Symbols = append(request.Symbols, query.SymbolSelector{
			ModuleKey: moduleKey, PackagePath: ref.ImportPath, Kind: kind.String(), Owner: ref.Recv, Name: ref.Name,
		})
	}
	pipeline, err := query.NewPipeline(database)
	if err != nil {
		return nil, err
	}
	locations, err := pipeline.FindReferences(ctx, request)
	if err != nil {
		return nil, err
	}
	for _, location := range locations {
		if _, _, err := indexedSource(location); err != nil {
			return nil, err
		}
	}
	return locations, nil
}

func indexedSource(location query.ReferenceLocation) (string, []byte, error) {
	root, err := filepath.EvalSymlinks(location.Location)
	if err != nil {
		return "", nil, err
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(location.Path)))
	if err != nil {
		return "", nil, err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("indexed path %q escapes module %q", location.Path, root)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	hash := sha256.Sum256(content)
	if hex.EncodeToString(hash[:]) != location.SourceHash {
		return "", nil, fmt.Errorf("stale UIR source %s; run uir reindex", path)
	}
	if location.StartByte < 0 || location.EndByte > len(content) || location.EndByte <= location.StartByte {
		return "", nil, fmt.Errorf("invalid UIR reference span %s:%d-%d", path, location.StartByte, location.EndByte)
	}
	return path, content, nil
}
