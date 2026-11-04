// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cli

import (
	"fmt"
	"strconv"
	"time"

	"github.com/woozymasta/inverz"
	cliprogress "github.com/woozymasta/inverz/internal/cli/progress"
	"github.com/woozymasta/inverz/oci"
)

const (
	// defaultRepository is the canonical source for published Inverz index artifacts.
	defaultRepository = "ghcr.io/woozymasta/inverz"
)

// Execute builds an index profile or prints its resource estimate.
func (c *GenerateCommand) Execute(_ []string) error {
	a, namespace, algorithm := c.app, c.namespace, c.algorithm
	p, err := namespace.resolve()
	if err != nil {
		return err
	}
	options := inverz.BuildOptions{
		Algorithm: algorithm,
		Force:     namespace.Force,
		Range:     inverz.SteamIDRange{Min: namespace.MinSteamID, Max: namespace.MaxSteamID},
		Profile:   p,
		Jobs:      namespace.Jobs,
	}
	if namespace.Estimate {
		estimate, err := inverz.Estimate(options)
		if err != nil {
			return err
		}
		return printEstimate(a.stdout, algorithm, p, estimate)
	}

	indexRoot, _ := a.dataPaths()
	session := cliprogress.New(a.stderr, "generate", "building index")
	options.OnProgress = session.Report
	startedAt := time.Now()
	result, err := inverz.Build(a.ctx, indexRoot, options)
	session.CloseWithoutSummary(err)
	if err != nil {
		return err
	}

	if session.GeneratedSegments() == 0 {
		return writeKeyValues(a.stdout, [][2]string{
			{"status", "skipped"},
			{"algorithm", algorithm.String()},
			{"profile", p.String()},
			{"reason", "all requested segments already exist"},
			{"action", "use --force to rebuild"},
		})
	}

	elapsed := time.Since(startedAt)
	averageRate := float64(session.GeneratedEntries()) / elapsed.Seconds()
	return printBuildSummary(
		a.stdout, algorithm, p, namespace, result, elapsed,
		session.GeneratedSegments(), averageRate,
	)
}

// Execute hashes one or more SteamIDs into DayZ identifiers.
func (c *HashDayZCommand) Execute(_ []string) error {
	a, namespace := c.app, c.namespace
	items := make([]hashResult, 0, len(c.IDs.Values))
	for _, id := range c.IDs.Values {
		items = append(items, hashResult{SteamID: id, DayZID: inverz.DayZID(id)})
	}

	return writeHashResults(a.stdout, namespace.Format, items, true)
}

// Execute hashes one or more SteamIDs into BattlEye GUIDs.
func (c *HashBattleyeCommand) Execute(_ []string) error {
	a, namespace := c.app, c.namespace
	items := make([]hashResult, 0, len(c.IDs.Values))
	for _, id := range c.IDs.Values {
		items = append(items, hashResult{SteamID: id, BattleyeGUID: inverz.BattlEyeGUID(id)})
	}

	return writeHashResults(a.stdout, namespace.Format, items, false)
}

// Execute resolves DayZ identifiers against one local profile and the shared cache.
func (c *QueryDayZCommand) Execute(_ []string) error {
	a, namespace := c.app, c.namespace
	inputs := make([]queryInput, len(c.IDs.Values))
	for i, value := range c.IDs.Values {
		inputs[i] = queryInput(value)
	}

	return a.query(inverz.AlgorithmDayZ, namespace.ProfileOptions, namespace.Format, namespace.NoCache, inputs)
}

// Execute resolves BattlEye GUIDs against one local profile and the shared cache.
func (c *QueryBattleyeCommand) Execute(_ []string) error {
	a, namespace := c.app, c.namespace
	inputs := make([]queryInput, len(c.IDs.Values))
	for i, value := range c.IDs.Values {
		inputs[i] = queryInput(value)
	}

	return a.query(inverz.AlgorithmBattlEye, namespace.ProfileOptions, namespace.Format, namespace.NoCache, inputs)
}

// Execute prints manifest, segment and remote metadata without whole-file hashing.
func (c *InfoCommand) Execute(_ []string) error {
	a, namespace, algorithm := c.app, c.namespace, c.algorithm
	options := namespace.ProfileOptions
	p, err := options.resolve()
	if err != nil {
		return err
	}

	indexRoot, _ := a.dataPaths()
	info, err := inverz.Inspect(indexRoot, algorithm, p)
	if err != nil {
		return profileMissingError(algorithm, p, err)
	}
	if namespace.Format == "json" {
		return encodeJSON(a.stdout, cliInfoJSON(info))
	}

	return printInfo(a.stdout, info)
}

// Execute opens the selected index and performs complete integrity verification.
func (c *VerifyCommand) Execute(_ []string) error {
	a, namespace, algorithm := c.app, c.namespace, c.algorithm
	options := namespace.ProfileOptions
	p, err := options.resolve()
	if err != nil {
		return err
	}

	indexRoot, _ := a.dataPaths()
	info, err := inverz.Inspect(indexRoot, algorithm, p)
	if err != nil {
		return profileMissingError(algorithm, p, err)
	}

	session := cliprogress.New(a.stderr, "verify", fmt.Sprintf("%s-%s integrity", algorithm, p.String()))
	err = inverz.VerifyWithProgress(indexRoot, algorithm, p, session.Report)
	session.CloseWithoutSummary(err)
	if err != nil {
		return fmt.Errorf("verify %s/%s: %w", algorithm, p.String(), err)
	}

	return writeKeyValues(a.stdout, [][2]string{
		{"status", "verified"},
		{"algorithm", algorithm.String()},
		{"profile", p.String()},
		{"segments", strconv.Itoa(info.SegmentCount)},
	})
}

// Execute removes the selected profile using a canonical algorithm/profile path.
func (c *RemoveCommand) Execute(_ []string) error {
	a, namespace, algorithm := c.app, c.namespace, c.algorithm
	options := namespace.ProfileOptions
	p, err := options.resolve()
	if err != nil {
		return err
	}

	indexRoot, _ := a.dataPaths()
	if err := inverz.Remove(indexRoot, algorithm, p); err != nil {
		return err
	}

	return writeKeyValues(a.stdout, [][2]string{
		{"status", "removed"},
		{"algorithm", algorithm.String()},
		{"profile", p.String()},
	})
}

// Execute lists installed profiles using bounded manifest metadata only.
func (c *ListCommand) Execute(_ []string) error {
	a := c.app
	indexRoot, _ := a.dataPaths()
	releases, err := inverz.List(indexRoot)
	if err != nil {
		return err
	}

	if c.Format == "json" {
		return encodeJSON(a.stdout, releases)
	}

	return printReleaseTable(a.stdout, releases)
}

// Execute synchronizes the selected profile from the configured OCI repository.
func (c *PullCommand) Execute(_ []string) error {
	a, namespace, algorithm := c.app, c.namespace, c.algorithm
	options := namespace.ProfileOptions
	p, err := options.resolve()
	if err != nil {
		return err
	}

	repository := c.Repository.Value
	if repository == "" {
		repository = defaultRepository
	}
	tag, err := p.Tag(algorithm)
	if err != nil {
		return err
	}

	indexRoot, _ := a.dataPaths()
	client, err := oci.New(oci.WithAuthConfig(namespace.AuthConfig), oci.WithPlainHTTP(namespace.PlainHTTP))
	if err != nil {
		return err
	}

	session := cliprogress.New(a.stderr, "pull", fmt.Sprintf("%s-%s", algorithm, p.String()))
	digest, err := client.PullWithProgress(a.ctx, indexRoot, repository, algorithm, p, session.Report)
	session.CloseWithoutSummary(err)
	if err != nil {
		return err
	}

	if namespace.PlainHTTP {
		_, _ = fmt.Fprintln(a.stderr, "warning: registry traffic uses unencrypted HTTP")
	}

	return writeKeyValues(a.stdout, [][2]string{
		{"status", "pulled"},
		{"reference", repository + ":" + tag},
		{"digest", digest},
	})
}

// Execute validates the local release and delegates publication to ocireg.
func (c *PushCommand) Execute(_ []string) error {
	a, namespace, algorithm := c.app, c.namespace, c.algorithm
	options := namespace.ProfileOptions
	p, err := options.resolve()
	if err != nil {
		return err
	}

	tag, err := p.Tag(algorithm)
	if err != nil {
		return err
	}

	repository := c.Repository.Value
	indexRoot, _ := a.dataPaths()
	if namespace.PlainHTTP {
		_, _ = fmt.Fprintln(a.stderr, "warning: registry traffic uses unencrypted HTTP")
	}
	client, err := oci.New(oci.WithAuthConfig(namespace.AuthConfig), oci.WithPlainHTTP(namespace.PlainHTTP))
	if err != nil {
		return err
	}

	session := cliprogress.New(a.stderr, "push", fmt.Sprintf("%s-%s", algorithm, p.String()))
	digest, err := client.Push(a.ctx, indexRoot, repository, algorithm, p, oci.PushOptions{
		AllowPartial: namespace.AllowPartial,
		OnProgress:   session.Report,
	})
	session.CloseWithoutSummary(err)
	if err != nil {
		return err
	}

	return writeKeyValues(a.stdout, [][2]string{
		{"status", "published"},
		{"reference", repository + ":" + tag},
		{"digest", digest},
	})
}

// Execute lists canonical index artifacts in the selected remote repository.
func (c *RemoteListCommand) Execute(_ []string) error {
	a := c.app
	repository := c.Repository.Value
	if repository == "" {
		repository = defaultRepository
	}
	if c.PlainHTTP {
		_, _ = fmt.Fprintln(a.stderr, "warning: registry traffic uses unencrypted HTTP")
	}
	client, err := oci.New(oci.WithAuthConfig(c.AuthConfig), oci.WithPlainHTTP(c.PlainHTTP))
	if err != nil {
		return err
	}

	artifacts, err := client.List(a.ctx, repository)
	if err != nil {
		return err
	}
	if c.Format == "json" {
		return encodeJSON(a.stdout, artifacts)
	}

	return printArtifactTable(a.stdout, artifacts)
}
