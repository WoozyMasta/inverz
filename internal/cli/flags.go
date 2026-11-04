// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: Copyright 2024-2026 WoozyMasta
// Source: https://github.com/WoozyMasta/inverz

package cli

import "github.com/woozymasta/inverz"

// ProfileOptions selects the canonical index codec profile.
type ProfileOptions struct {
	RiceK       int   `short:"k" default:"-1" long:"rice-k" description:"Rice parameter; omitted value is prefix-bits minus one" default-mask:"-"`
	PrefixBits  uint8 `short:"p" default:"18" long:"prefix-bits" description:"Digest prefix width" validate-min:"1" validate-max:"20"`
	SegmentBits uint8 `short:"s" default:"28" long:"segment-bits" description:"AccountID segment width" validate-min:"1" validate-max:"32"`
}

// FormatOptions selects human-readable or JSON output.
type FormatOptions struct {
	Format string `short:"f" long:"format" default:"text" choices:"text;json" description:"Output format"`
}

// RegistryOptions configures existing OCI authentication and transport behavior.
type RegistryOptions struct {
	AuthConfig string `long:"registry-auth" description:"Docker-compatible registry auth config path" completion:"file"`
	PlainHTTP  bool   `long:"plain-http"    description:"Allow unencrypted HTTP registry traffic" auto-env:"false"`
}

// GenerateRangeOptions selects the SteamID range and generator CPU budget.
type GenerateRangeOptions struct {
	MinSteamID uint64 `short:"m" long:"min-steamid" default:"76561197960265728" validate-min:"76561197960265728" description:"Minimum SteamID64" validate-max:"76561202255233023"`
	MaxSteamID uint64 `short:"M" long:"max-steamid" default:"76561200107749375" validate-min:"76561197960265728" description:"Maximum SteamID64" validate-max:"76561202255233023"`
	Jobs       int    `short:"j" long:"jobs" default:"0" validate-min:"0" description:"Total generator CPU budget; zero uses GOMAXPROCS"`
}

// GenerateNamespace owns settings shared by both algorithm-specific generators.
type GenerateNamespace struct { //betteralign:ignore
	DayZ     GenerateCommand `description:"Generate a DayZ index" aliases:"dz" command:"dayz"`
	Battleye GenerateCommand `description:"Generate a BattlEye index" aliases:"be" command:"battleye"`
	Estimate bool            `description:"Print resource estimates without generating files" long:"estimate" short:"e"`
	Force    bool            `description:"Rebuild existing segments instead of reusing them" long:"force" auto-env:"false"`

	GenerateRangeOptions
	ProfileOptions
}

// GenerateCommand builds an algorithm-specific local index profile.
type GenerateCommand struct {
	app       *App               `no-flag:"true"`
	namespace *GenerateNamespace `no-flag:"true"`
	algorithm inverz.Algorithm   `no-flag:"true"`
}

// HashNamespace owns output settings shared by both identifier generators.
type HashNamespace struct {
	FormatOptions

	DayZ     HashDayZCommand     `aliases:"dz" command:"dayz"     description:"Calculate DayZ identifiers"`
	Battleye HashBattleyeCommand `aliases:"be" command:"battleye" description:"Calculate BattlEye GUIDs"`
}

// HashDayZCommand calculates padded DayZ identifiers for one or more SteamIDs.
type HashDayZCommand struct {
	app       *App           `no-flag:"true"`
	namespace *HashNamespace `no-flag:"true"`

	IDs struct {
		Values []uint64 `required:"1-" positional-arg-name:"STEAMID" description:"SteamID64 values" validate-min:"76561197960265728" validate-max:"76561202255233023"`
	} `positional-args:"yes"`
}

// HashBattleyeCommand calculates lowercase BattlEye GUIDs for SteamIDs.
type HashBattleyeCommand struct {
	app       *App           `no-flag:"true"`
	namespace *HashNamespace `no-flag:"true"`

	IDs struct {
		Values []uint64 `required:"1-" positional-arg-name:"STEAMID" description:"SteamID64 values" validate-min:"76561197960265728" validate-max:"76561202255233023"`
	} `positional-args:"yes"`
}

// QueryNamespace owns output, profile, and cache settings shared by lookups.
type QueryNamespace struct { //betteralign:ignore
	DayZ     QueryDayZCommand     `description:"Resolve DayZ identifiers"        aliases:"dz" command:"dayz"`
	Battleye QueryBattleyeCommand `description:"Resolve BattlEye GUIDs"          aliases:"be" command:"battleye"`
	NoCache  bool                 `description:"Disable the shared SQLite cache" long:"no-cache"`

	FormatOptions
	ProfileOptions
}

// QueryDayZCommand resolves one or more DayZ identifiers from local data.
type QueryDayZCommand struct {
	app       *App            `no-flag:"true"`
	namespace *QueryNamespace `no-flag:"true"`

	IDs struct {
		Values []DayZValue `required:"1-" positional-arg-name:"ID" description:"DayZ identifiers"`
	} `positional-args:"yes"`
}

// QueryBattleyeCommand resolves one or more BattlEye GUIDs from local data.
type QueryBattleyeCommand struct {
	app       *App            `no-flag:"true"`
	namespace *QueryNamespace `no-flag:"true"`

	IDs struct {
		Values []BattleyeValue `required:"1-" positional-arg-name:"GUID" description:"BattlEye GUIDs"`
	} `positional-args:"yes"`
}

// PullNamespace owns registry and profile settings shared by both algorithms.
type PullNamespace struct {
	DayZ     PullCommand `aliases:"dz" command:"dayz"     description:"Synchronize a DayZ index"`
	Battleye PullCommand `aliases:"be" command:"battleye" description:"Synchronize a BattlEye index"`

	RegistryOptions
	ProfileOptions
}

// PullCommand synchronizes one canonical profile from an optional OCI repository.
type PullCommand struct {
	app       *App           `no-flag:"true"`
	namespace *PullNamespace `no-flag:"true"`

	Repository struct {
		Value string `positional-arg-name:"REPOSITORY" description:"OCI repository; defaults to the Inverz GHCR repository"`
	} `positional-args:"yes"`
	algorithm inverz.Algorithm `no-flag:"true"`
}

// PushNamespace owns registry, profile, and publication settings shared by both algorithms.
type PushNamespace struct { //betteralign:ignore
	DayZ         PushCommand `description:"Publish a DayZ index" aliases:"dz" command:"dayz"`
	Battleye     PushCommand `description:"Publish a BattlEye index" aliases:"be" command:"battleye"`
	AllowPartial bool        `description:"Permit publication of partial segments; use only for development data" auto-env:"false" long:"allow-partial"`

	RegistryOptions
	ProfileOptions
}

// PushCommand publishes one local profile to an explicit OCI repository.
type PushCommand struct {
	app        *App           `no-flag:"true"`
	namespace  *PushNamespace `no-flag:"true"`
	Repository struct {
		Value string `required:"yes" validate-non-empty:"true" positional-arg-name:"REPOSITORY" description:"Destination OCI repository"`
	} `positional-args:"yes"`
	algorithm inverz.Algorithm `no-flag:"true"`
}

// InfoNamespace owns output and profile settings shared by metadata commands.
type InfoNamespace struct {
	DayZ     InfoCommand `aliases:"dz" command:"dayz"     description:"Show DayZ index metadata"`
	Battleye InfoCommand `aliases:"be" command:"battleye" description:"Show BattlEye index metadata"`

	FormatOptions
	ProfileOptions
}

// InfoCommand prints detailed metadata for one local profile.
type InfoCommand struct {
	app       *App             `no-flag:"true"`
	namespace *InfoNamespace   `no-flag:"true"`
	algorithm inverz.Algorithm `no-flag:"true"`
}

// VerifyNamespace owns profile settings shared by integrity commands.
type VerifyNamespace struct {
	DayZ     VerifyCommand `aliases:"dz" command:"dayz"     description:"Verify a DayZ index"`
	Battleye VerifyCommand `aliases:"be" command:"battleye" description:"Verify a BattlEye index"`
	ProfileOptions
}

// VerifyCommand performs complete integrity verification for one local profile.
type VerifyCommand struct {
	app       *App             `no-flag:"true"`
	namespace *VerifyNamespace `no-flag:"true"`
	algorithm inverz.Algorithm `no-flag:"true"`
}

// RemoveNamespace owns profile settings shared by removal commands.
type RemoveNamespace struct {
	DayZ     RemoveCommand `aliases:"dz" command:"dayz"     description:"Remove a DayZ index profile"`
	Battleye RemoveCommand `aliases:"be" command:"battleye" description:"Remove a BattlEye index profile"`
	ProfileOptions
}

// RemoveCommand removes exactly one canonical profile from the local store.
type RemoveCommand struct {
	app       *App             `no-flag:"true"`
	namespace *RemoveNamespace `no-flag:"true"`
	algorithm inverz.Algorithm `no-flag:"true"`
}

// ListCommand lists installed profiles without hashing segment contents.
type ListCommand struct {
	app *App `no-flag:"true"`
	FormatOptions
}

// RemoteNamespace contains remote registry discovery commands.
type RemoteNamespace struct {
	List RemoteListCommand `aliases:"ls" command:"list" description:"List canonical index artifacts in a repository"`
}

// RemoteListCommand lists remote artifact metadata without downloading layers.
type RemoteListCommand struct {
	app *App `no-flag:"true"`
	FormatOptions
	Repository struct {
		Value string `positional-arg-name:"REPOSITORY" description:"OCI repository; defaults to the Inverz GHCR repository"`
	} `positional-args:"yes"`
	RegistryOptions
}

// resolve converts parsed profile flags into the validated canonical domain value.
func (o ProfileOptions) resolve() (inverz.Profile, error) {
	riceK := o.RiceK
	if riceK == -1 {
		riceK = int(o.PrefixBits) - 1
	}
	if riceK < 0 || riceK > 31 {
		return inverz.Profile{}, errInvalidRiceK
	}

	p := inverz.Profile{
		Version:     1,
		PrefixBits:  o.PrefixBits,
		RiceK:       uint8(riceK),
		SegmentBits: o.SegmentBits,
	}
	if err := p.Validate(); err != nil {
		return inverz.Profile{}, err
	}

	return p, nil
}
