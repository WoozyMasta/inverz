// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

// Package cli defines the Inverz command tree and orchestrates existing core services.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/woozymasta/flags"
	"github.com/woozymasta/inverz"
	"github.com/woozymasta/inverz/internal/appdirs"
)

const (
	// maxCacheBytes is the largest size accepted by the SQLite cache.
	maxCacheBytes int64 = 1<<63 - 1
	// partialFailureExitCode reports a completed batch with item-level failures.
	partialFailureExitCode = 3
)

var (
	// version overrides the module version when the release build provides one.
	version string
	// commit overrides the VCS revision when the release build provides one.
	commit string
	// buildTimeOverride overrides the VCS timestamp in RFC3339 format.
	buildTimeOverride string
	// repositoryURL overrides the URL inferred from the module path.
	repositoryURL string
)

// exitError carries an operational result code without duplicating command diagnostics.
type exitError struct {
	Err  error
	Code int
}

// App holds global options and the complete product command tree.
type App struct { //betteralign:ignore
	DataDir      string   `long:"data-dir"       description:"Application data directory" short:"d"`
	CacheMaxSize ByteSize `long:"cache-max-size" description:"Maximum SQLite cache footprint (KiB, MiB, or GiB)" default:"512MiB"`

	Generate GenerateNamespace `command:"generate" description:"Build a local index profile" aliases:"gen"`
	Hash     HashNamespace     `command:"hash"     description:"Calculate identifiers from SteamID64"`
	Query    QueryNamespace    `command:"query"    description:"Resolve identifiers through a local index"`
	Pull     PullNamespace     `command:"pull"     description:"Synchronize an index profile from OCI"`
	Push     PushNamespace     `command:"push"     description:"Publish a local index profile to OCI"`
	Info     InfoNamespace     `command:"info"     description:"Show local index profile metadata"`
	Verify   VerifyNamespace   `command:"verify"   description:"Verify a local index profile"`
	Remove   RemoveNamespace   `command:"rm"       description:"Remove one local index profile" aliases:"remove"`
	List     ListCommand       `command:"list"     description:"List installed local index profiles" aliases:"ls"`
	Remote   RemoteNamespace   `command:"remote"   description:"Inspect remote index artifacts"`

	ctx          context.Context `no-flag:"true"`
	stdout       io.Writer       `no-flag:"true"`
	stderr       io.Writer       `no-flag:"true"`
	cacheWarning sync.Once       `no-flag:"true"`
}

// Error returns the underlying command failure text.
func (e *exitError) Error() string {
	return e.Err.Error()
}

// Unwrap exposes the underlying command failure for errors.Is and errors.As.
func (e *exitError) Unwrap() error {
	return e.Err
}

// Run parses the product CLI, executes a leaf and maps failures to stable exit codes.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	paths, err := appdirs.Default()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}

	return runWithPaths(ctx, args, stdout, stderr, paths)
}

// runWithPaths executes the parser with already resolved platform paths.
func runWithPaths(ctx context.Context, args []string, stdout, stderr io.Writer, paths appdirs.Paths) int {
	app := newApp(ctx, stdout, stderr, paths)
	parser := newParser(app, paths)
	commandExecuted := false
	parser.CommandHandler = func(command flags.Commander, args []string) error {
		if command == nil {
			return nil
		}

		commandExecuted = true
		return command.Execute(args)
	}

	if _, err := parser.ParseArgs(args); err != nil {
		if parserErr, ok := errors.AsType[*flags.Error](err); ok {
			if parserErr.Type == flags.ErrHelp || parserErr.Type == flags.ErrVersion {
				return 0
			}
			return 2
		}

		if exitErr, ok := errors.AsType[*exitError](err); ok {
			return exitErr.Code
		}
		if !commandExecuted {
			return 2
		}

		return 1
	}

	return 0
}

// newApp creates a command tree and binds each leaf command to its app and namespace.
func newApp(ctx context.Context, stdout, stderr io.Writer, paths appdirs.Paths) *App {
	a := &App{
		DataDir: paths.DataRoot,
		ctx:     ctx,
		stdout:  stdout,
		stderr:  stderr,
	}

	a.Generate.DayZ.app, a.Generate.Battleye.app = a, a
	a.Generate.DayZ.namespace, a.Generate.Battleye.namespace = &a.Generate, &a.Generate
	a.Generate.DayZ.algorithm, a.Generate.Battleye.algorithm = inverz.AlgorithmDayZ, inverz.AlgorithmBattlEye
	a.Hash.DayZ.app, a.Hash.Battleye.app = a, a
	a.Hash.DayZ.namespace, a.Hash.Battleye.namespace = &a.Hash, &a.Hash
	a.Query.DayZ.app, a.Query.Battleye.app = a, a
	a.Query.DayZ.namespace, a.Query.Battleye.namespace = &a.Query, &a.Query
	a.Pull.DayZ.app, a.Pull.Battleye.app = a, a
	a.Pull.DayZ.namespace, a.Pull.Battleye.namespace = &a.Pull, &a.Pull
	a.Pull.DayZ.algorithm, a.Pull.Battleye.algorithm = inverz.AlgorithmDayZ, inverz.AlgorithmBattlEye
	a.Push.DayZ.app, a.Push.Battleye.app = a, a
	a.Push.DayZ.namespace, a.Push.Battleye.namespace = &a.Push, &a.Push
	a.Push.DayZ.algorithm, a.Push.Battleye.algorithm = inverz.AlgorithmDayZ, inverz.AlgorithmBattlEye
	a.Info.DayZ.app, a.Info.Battleye.app = a, a
	a.Info.DayZ.namespace, a.Info.Battleye.namespace = &a.Info, &a.Info
	a.Info.DayZ.algorithm, a.Info.Battleye.algorithm = inverz.AlgorithmDayZ, inverz.AlgorithmBattlEye
	a.Verify.DayZ.app, a.Verify.Battleye.app = a, a
	a.Verify.DayZ.namespace, a.Verify.Battleye.namespace = &a.Verify, &a.Verify
	a.Verify.DayZ.algorithm, a.Verify.Battleye.algorithm = inverz.AlgorithmDayZ, inverz.AlgorithmBattlEye
	a.Remove.DayZ.app, a.Remove.Battleye.app = a, a
	a.Remove.DayZ.namespace, a.Remove.Battleye.namespace = &a.Remove, &a.Remove
	a.Remove.DayZ.algorithm, a.Remove.Battleye.algorithm = inverz.AlgorithmDayZ, inverz.AlgorithmBattlEye
	a.List.app = a
	a.Remote.List.app = a

	return a
}

// newParser configures strict parsing, safe dotenv defaults and plain help output.
func newParser(app *App, paths appdirs.Paths) *flags.Parser {
	parser := flags.NewNamedParser("inverz",
		flags.Default|
			flags.VersionFlag|
			flags.StrictCommands|
			flags.StrictPositionalArgs|
			flags.EnvProvisioning|
			flags.ShowCommandAliases|
			flags.ShowRepeatableInHelp|
			flags.PrintHelpOnInputErrors|
			flags.HelpCommand|
			flags.VersionCommand|
			flags.CompletionCommand|
			flags.DocsCommand|
			flags.DetectShellFlagStyle|
			flags.DetectShellEnvStyle|
			flags.DotEnvFlags)
	if _, err := parser.AddGroup("Application Options", "Global application options", app); err != nil {
		panic(fmt.Errorf("register CLI options: %w", err))
	}

	parser.SetEnvPrefix("INVERZ")
	if paths.DotEnv != "" {
		parser.SetDotEnvFile(paths.DotEnv)
	}
	parser.SetHelpHeader("Reverse DayZ and BattlEye identifiers through local indexes.")

	versionInfo := flags.ReadVersionInfo()
	if version != "" {
		versionInfo.Version = version
	}
	if commit != "" {
		versionInfo.Revision = commit
	}
	if buildTime, err := time.Parse(time.RFC3339, buildTimeOverride); err == nil {
		versionInfo.RevisionTime = buildTime
	}
	if repositoryURL != "" {
		versionInfo.URL = repositoryURL
	}
	parser.SetVersionInfo(versionInfo)
	parser.SetVersionURL(versionInfo.URL)
	parser.SetVersionFields(flags.VersionFieldsAll)
	if err := configureExamples(parser, app); err != nil {
		panic(fmt.Errorf("configure CLI examples: %w", err))
	}

	return parser
}

// configureExamples adds examples for useful multi-value, JSON, and profile-selection workflows.
func configureExamples(parser *flags.Parser, app *App) error {
	entries := []struct {
		command  any
		examples []*flags.CommandExample
	}{
		{
			command: &app.Generate,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Build a small DayZ profile").
					Option(&app.Generate.MaxSteamID, "76561197960265759").
					Arg("dayz"),
			},
		},
		{
			command: &app.Generate.Battleye,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Estimate the default BattlEye profile before building").
					Option(&app.Generate.Estimate),
			},
		},
		{
			command: &app.Generate.DayZ,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Estimate a DayZ profile before building").
					Option(&app.Generate.Estimate),
			},
		},
		{
			command: &app.Hash.DayZ,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Hash several SteamIDs as text").
					Arg("76561197960265728").
					Arg("76561197960265729").
					Arg("76561197960265730"),
				flags.Example().
					Describe("Extract DayZ IDs from batch JSON output").
					Option(&app.Hash.Format, "json").
					Arg("76561197960265728").
					Arg("76561197960265729").
					Arg("76561197960265730").
					Raw("| jq -r '.[].dayz_id'"),
			},
		},
		{
			command: &app.Hash.Battleye,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Hash several SteamIDs as text").
					Arg("76561197960265728").
					Arg("76561197960265729").
					Arg("76561197960265730"),
				flags.Example().
					Describe("Extract BattlEye GUIDs from batch JSON output").
					Option(&app.Hash.Format, "json").
					Arg("76561197960265728").
					Arg("76561197960265729").
					Arg("76561197960265730").
					Raw("| jq -r '.[].battleye_guid'"),
			},
		},
		{
			command: &app.Query.DayZ,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Resolve several DayZ IDs in one request").
					Arg("JCNShusG41ALBHLwqmAu5wINMUMVGiGONjUqccYdh4I=").
					Arg("ccuWIpEIrX_wmD5VsthErf1qNQbEK7IdG6fL1Il4KuA=").
					Arg("sLQO54uQJFcCxgGmRpx6sraXjrZV2P9b_zj1Wli4bhs="),
				flags.Example().
					Describe("Keep resolved SteamIDs from batch JSON output").
					Option(&app.Query.Format, "json").
					Arg("JCNShusG41ALBHLwqmAu5wINMUMVGiGONjUqccYdh4I=").
					Arg("ccuWIpEIrX_wmD5VsthErf1qNQbEK7IdG6fL1Il4KuA=").
					Raw("| jq -r '.[] | .steam_id // empty'"),
			},
		},
		{
			command: &app.Query.Battleye,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Resolve several BattlEye GUIDs in one request").
					Arg("4fc867abf98b934e9e7eeaf15170258c").
					Arg("74ae012b5407e0a3cc2cd82ec1f8ba7d").
					Arg("0cc99e3ab77a8a2037d278816f3c9ca7"),
				flags.Example().
					Describe("Keep resolved SteamIDs from batch JSON output").
					Option(&app.Query.Format, "json").
					Arg("4fc867abf98b934e9e7eeaf15170258c").
					Arg("74ae012b5407e0a3cc2cd82ec1f8ba7d").
					Raw("| jq -r '.[] | .steam_id // empty'"),
			},
		},
		{
			command: &app.Push.DayZ,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Publish a non-default DayZ profile to GHCR").
					Option(&app.Push.PrefixBits, "16").
					Option(&app.Push.SegmentBits, "24").
					Arg("ghcr.io/woozymasta/inverz"),
			},
		},
		{
			command: &app.Push.Battleye,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Publish a non-default BattlEye profile to GHCR").
					Option(&app.Push.PrefixBits, "16").
					Option(&app.Push.SegmentBits, "24").
					Arg("ghcr.io/woozymasta/inverz"),
			},
		},
		{
			command: &app.Pull.DayZ,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Pull a non-default DayZ profile from GHCR").
					Option(&app.Pull.PrefixBits, "16").
					Option(&app.Pull.SegmentBits, "24"),
			},
		},
		{
			command: &app.Pull.Battleye,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Pull a non-default BattlEye profile from GHCR").
					Option(&app.Pull.PrefixBits, "16").
					Option(&app.Pull.SegmentBits, "24"),
			},
		},
		{
			command: &app.Info.DayZ,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Select key metadata fields as JSON").
					Option(&app.Info.Format, "json").
					Raw("| jq '{algorithm, profile, entry_count}'"),
			},
		},
		{
			command: &app.Info.Battleye,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Select key metadata fields as JSON").
					Option(&app.Info.Format, "json").
					Raw("| jq '{algorithm, profile, entry_count}'"),
			},
		},
		{
			command: &app.Verify.DayZ,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Verify a non-default DayZ profile").
					Option(&app.Verify.PrefixBits, "16").
					Option(&app.Verify.SegmentBits, "24"),
			},
		},
		{
			command: &app.Verify.Battleye,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Verify a non-default BattlEye profile").
					Option(&app.Verify.PrefixBits, "16").
					Option(&app.Verify.SegmentBits, "24"),
			},
		},
		{
			command: &app.Remove.DayZ,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Remove a non-default DayZ profile").
					Option(&app.Remove.PrefixBits, "16").
					Option(&app.Remove.SegmentBits, "24"),
			},
		},
		{
			command: &app.Remove.Battleye,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("Remove a non-default BattlEye profile").
					Option(&app.Remove.PrefixBits, "16").
					Option(&app.Remove.SegmentBits, "24"),
			},
		},
		{
			command: &app.List,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("List installed profiles as tab-separated JSON fields").
					Option(&app.List.Format, "json").
					Raw("| jq -r '.[] | [.algorithm, .profile, .state] | @tsv'"),
			},
		},
		{
			command: &app.Remote.List,
			examples: []*flags.CommandExample{
				flags.Example().
					Describe("List published algorithms and tags from GHCR").
					Option(&app.Remote.List.Format, "json").
					Arg("ghcr.io/woozymasta/inverz").
					Raw("| jq -r '.[] | [.algorithm, .tag] | @tsv'"),
			},
		},
	}
	for _, entry := range entries {
		command, err := parser.CommandFor(entry.command)
		if err != nil {
			return err
		}
		if err := command.SetExamples(entry.examples...); err != nil {
			return err
		}
	}

	return nil
}

// partialFailure reports that every batch input was handled with some logical lookup failures.
func partialFailure() error {
	return &exitError{Code: partialFailureExitCode, Err: errors.New("one or more query inputs failed")}
}

// dataPaths resolves the application index root and shared cache location.
func (a *App) dataPaths() (string, string) {
	return filepath.Join(a.DataDir, "indexes"), filepath.Join(a.DataDir, "cache.sqlite")
}
