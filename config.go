package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const configName = ".gopatch.yml"

type consumerOptions struct {
	Consumers     []string
	ConsumersFrom string
}

type consumerSelection struct {
	Roots      []string
	Explicit   bool
	Configured bool
}

type gopatchConfig struct {
	Consumers []string `yaml:"consumers"`
}

func (cmd *mainCmd) consumerRoots(producerRoot string, opts *consumerOptions) (consumerSelection, error) {
	if len(opts.Consumers) > 0 || opts.ConsumersFrom != "" {
		roots, err := cmd.cliConsumerRoots(opts)
		return consumerSelection{Roots: roots, Explicit: true}, err
	}

	project := filepath.Join(producerRoot, configName)
	if exists, err := configExists(project); err != nil {
		return consumerSelection{}, err
	} else if exists {
		roots, err := cmd.readConsumerConfig(project)
		return consumerSelection{Roots: roots, Configured: true}, err
	}

	home, err := cmd.UserHomeDir()
	if err != nil {
		return consumerSelection{}, fmt.Errorf("locate home config: %w", err)
	}
	homeConfig := filepath.Join(home, configName)
	if exists, err := configExists(homeConfig); err != nil {
		return consumerSelection{}, err
	} else if !exists {
		return consumerSelection{}, nil
	}
	roots, err := cmd.readConsumerConfig(homeConfig)
	return consumerSelection{Roots: roots, Configured: true}, err
}

func configExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, fmt.Errorf("stat config %v: %w", path, err)
	}
}

func (cmd *mainCmd) cliConsumerRoots(opts *consumerOptions) ([]string, error) {
	roots := append([]string(nil), opts.Consumers...)
	if opts.ConsumersFrom != "" {
		path, err := cmd.absPath(opts.ConsumersFrom)
		if err != nil {
			return nil, err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read --consumers-from %v: %w", path, err)
		}
		for _, line := range strings.Split(string(contents), "\n") {
			if root := strings.TrimSpace(line); root != "" {
				roots = append(roots, root)
			}
		}
	}
	if len(roots) == 0 {
		return nil, errors.New("consumer discovery needs at least one non-empty search directory")
	}
	cwd, err := cmd.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	return cmd.resolveConsumerPaths(roots, cwd)
}

func (cmd *mainCmd) readConsumerConfig(path string) ([]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %v: %w", path, err)
	}
	var config gopatchConfig
	decoder := yaml.NewDecoder(strings.NewReader(string(contents)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config %v: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return nil, fmt.Errorf("parse config %v: multiple YAML documents are not supported", path)
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config %v: %w", path, err)
	}
	return cmd.resolveConsumerPaths(config.Consumers, filepath.Dir(path))
}

func (cmd *mainCmd) resolveConsumerPaths(paths []string, relativeTo string) ([]string, error) {
	seen := make(map[string]bool, len(paths))
	var roots []string
	var home string
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			return nil, errors.New("consumer search directory cannot be empty")
		}
		if path == "~" || strings.HasPrefix(path, "~/") {
			if home == "" {
				var err error
				home, err = cmd.UserHomeDir()
				if err != nil {
					return nil, fmt.Errorf("locate home directory: %w", err)
				}
			}
			path = expandHome(path, home)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(relativeTo, path)
		}
		root, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("resolve consumer root %q: %w", path, err)
		}
		info, err := os.Stat(root)
		if err != nil {
			return nil, fmt.Errorf("stat consumer root %q: %w", root, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("consumer root %q is not a directory", root)
		}
		if !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	sort.Strings(roots)
	return roots, nil
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}
