/*
 * JuiceFS, Copyright 2021 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/pkg/errors"
	"github.com/urfave/cli/v2"
)

func cmdConfig() *cli.Command {
	return &cli.Command{
		Name:      "config",
		Action:    config,
		Category:  "ADMIN",
		Usage:     "Change configuration of a volume",
		ArgsUsage: "META-URL",
		Description: `
Only flags explicitly specified are changed.

Examples:
# Show the current configurations
$ juicefs config redis://localhost

# Change volume "quota"
$ juicefs config redis://localhost --inodes 10000000 --capacity 1048576

# Change maximum days before files in trash are deleted
$ juicefs config redis://localhost --trash-days 7

# Limit client version that is allowed to connect
$ juicefs config redis://localhost --min-client-version 1.0.0 --max-client-version 1.1.0`,
		Flags: expandFlags(
			formatStorageFlags(),
			addCategories("DATA STORAGE", []cli.Flag{
				&cli.StringFlag{
					Name:  "upload-limit",
					Usage: "default bandwidth limit of a client for upload in Mbps",
				},
				&cli.StringFlag{
					Name:  "download-limit",
					Usage: "default bandwidth limit of a client for download in Mbps",
				},
			}),
			formatManagementFlags(),
			configManagementFlags(),
			configFlags()),
	}
}

func configManagementFlags() []cli.Flag {
	return addCategories("MANAGEMENT", []cli.Flag{
		&cli.BoolFlag{
			Name:  "encrypt-secret",
			Usage: "encrypt the secret key if it was previously stored in plain format",
		},
		&cli.StringFlag{
			Name:  "min-client-version",
			Usage: "minimum client version allowed to connect",
		},
		&cli.StringFlag{
			Name:  "max-client-version",
			Usage: "maximum client version allowed to connect",
		},
		&cli.BoolFlag{
			Name:  "dir-stats",
			Usage: "enable dir stats, which is necessary for fast summary and dir quota",
		},
		&cli.BoolFlag{
			Name:  "user-group-quota",
			Usage: "enable user and group quota management",
		},
		&cli.BoolFlag{
			Name:  "changelog",
			Usage: "enable changelog",
		},
		&cli.DurationFlag{
			Name:        "changelog-max-age",
			Usage:       "max age of changelog entries (e.g. 2h, 30m); 0 to disable time-based cleanup",
			Value:       2 * time.Hour,
			DefaultText: "2h",
		},
		&cli.Int64Flag{
			Name:  "changelog-max-lines",
			Usage: "max number of changelog entries to keep; 0 means unlimited",
		},
		&cli.IntFlag{
			Name:  "tier",
			Usage: "tier (0-3; 0 is default tier when unset)",
			Action: func(ctx *cli.Context, v int) error {
				if !ctx.IsSet("tier") {
					return nil
				}
				if v < 0 || v > 3 {
					return fmt.Errorf("tier should be between 0 and 3")
				}
				return nil
			},
		},
	})
}

func configFlags() []cli.Flag {
	return []cli.Flag{
		&cli.BoolFlag{
			Name:    "yes",
			Aliases: []string{"y"},
			Usage:   "automatically answer 'yes' to all prompts and run non-interactively",
		},
		&cli.BoolFlag{
			Name:  "force",
			Usage: "skip sanity check and force update the configurations",
		},
	}
}

func warn(format string, a ...interface{}) {
	fmt.Printf("\033[1;33mWARNING\033[0m: "+format+"\n", a...)
}

func userConfirmed() bool {
	fmt.Print("Proceed anyway? [y/N]: ")
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if text := strings.ToLower(scanner.Text()); text == "y" || text == "yes" {
			return true
		} else if text == "" || text == "n" || text == "no" {
			return false
		} else {
			fmt.Print("Please input y(yes) or n(no): ")
		}
	}
	return false
}

func config(ctx *cli.Context) error {
	setup(ctx, 1)
	removePassword(ctx.Args().Get(0))
	m := meta.NewClient(ctx.Args().Get(0), nil)

	format, err := m.Load(false)
	if err != nil {
		return err
	}
	if len(ctx.LocalFlagNames()) == 0 {
		fmt.Println(format)
		return nil
	}

	originDirStats := format.DirStats
	originUGQuota := format.UserGroupQuota
	var quota, storage, trash, clientVer, tier bool
	var msg strings.Builder
	encrypted := format.KeyEncrypted
	var targetTierID uint8
	var currentTier object.Tier
	var findTier bool
	var newTier object.Tier

	var requiredMinClientVersion string
	requireMinClientVersion := func(required string) {
		requiredMinClientVersion = maxVersion(requiredMinClientVersion, required)
	}

	for _, flag := range ctx.LocalFlagNames() {
		switch flag {
		case "capacity":
			if new := utils.ParseBytes(ctx, flag, 'G'); new != format.Capacity {
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag,
					humanize.IBytes(format.Capacity), humanize.IBytes(new)))
				format.Capacity = new
				quota = true
			}
		case "inodes":
			if new := ctx.Uint64(flag); new != format.Inodes {
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag,
					humanize.Comma(int64(format.Inodes)), humanize.Comma(int64(new))))
				format.Inodes = new
				quota = true
			}
		case "storage":
			if new := ctx.String(flag); new != format.Storage {
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, format.Storage, new))
				format.Storage = new
				storage = true
			}
		case "bucket":
			// bucket will be accessed before storage, so it is necessary to determine if storage is a file
			if new := ctx.String(flag); new != format.Bucket {
				effectiveStorage := format.Storage
				if ctx.IsSet("storage") {
					effectiveStorage = ctx.String("storage")
				}
				if effectiveStorage == "file" {
					if p, err := filepath.Abs(new); err == nil {
						new = p + "/"
					} else {
						logger.Fatalf("Failed to get absolute path of %q: %s", new, err)
					}
				}
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, format.Bucket, new))
				format.Bucket = new
				storage = true
			}
		case "access-key":
			if ctx.IsSet("tier") {
				continue
			}
			if new := ctx.String(flag); new != format.AccessKey {
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, format.AccessKey, new))
				format.AccessKey = new
				storage = true
			}
		case "secret-key": // always update
			if ctx.IsSet("tier") {
				continue
			}
			msg.WriteString(fmt.Sprintf("%10s: updated\n", flag))
			if err := format.Decrypt(); err != nil && strings.Contains(err.Error(), "secret was removed") {
				logger.Warnf("decrypt secrets: %s", err)
			}
			format.SecretKey = ctx.String(flag)
			storage = true
		case "session-token": // always update
			if ctx.IsSet("tier") {
				continue
			}
			msg.WriteString(fmt.Sprintf("%10s: updated\n", flag))
			if err := format.Decrypt(); err != nil && strings.Contains(err.Error(), "secret was removed") {
				logger.Warnf("decrypt secrets: %s", err)
			}
			format.SessionToken = ctx.String(flag)
			storage = true
		case "storage-class": // always update
			if ctx.IsSet("tier") {
				continue
			}
			if new := ctx.String(flag); new != format.Tiers[0].Sc {
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, format.Tiers[0].Sc, new))
				newTier := format.Tiers[0]
				newTier.Sc = new
				format.Tiers[0] = newTier
				storage = true
				tier = true
			}
		case "tag":
			if ctx.IsSet("tier") {
				continue
			}
			new := ctx.String(flag)
			if !object.ValidateTag(new) {
				logger.Fatalf("Invalid tag format: %s", new)
			}
			if new != format.Tiers[0].Tag {
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, format.Tiers[0].Tag, new))
				newTier := format.Tiers[0]
				newTier.Tag = new
				format.Tiers[0] = newTier
				storage = true
				tier = true
			}
		case "upload-limit":
			if new := utils.ParseMbps(ctx, flag); new != format.UploadLimit {
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, utils.Mbps(format.UploadLimit), utils.Mbps(new)))
				format.UploadLimit = new
			}
		case "download-limit":
			if new := utils.ParseMbps(ctx, flag); new != format.DownloadLimit {
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, utils.Mbps(format.DownloadLimit), utils.Mbps(new)))
				format.DownloadLimit = new
			}
		case "trash-days":
			if new := ctx.Int(flag); new != format.TrashDays {
				if new < 0 {
					return fmt.Errorf("Invalid trash days: %d", new)
				}
				msg.WriteString(fmt.Sprintf("%10s: %d -> %d\n", flag, format.TrashDays, new))
				format.TrashDays = new
				trash = true
			}
		case "dir-stats":
			if new := ctx.Bool(flag); new != format.DirStats {
				msg.WriteString(fmt.Sprintf("%10s: %t -> %t\n", flag, format.DirStats, new))
				format.DirStats = new
			}
		case "changelog":
			if new := ctx.Bool(flag); new != format.ChangeLog {
				msg.WriteString(fmt.Sprintf("%10s: %t -> %t\n", flag, format.ChangeLog, new))
				format.ChangeLog = new
				if new && format.ChangeLogMaxAge == 0 && !ctx.IsSet("changelog-max-age") {
					format.ChangeLogMaxAge = 7200
					msg.WriteString(fmt.Sprintf("%10s: 0s -> %s (default)\n", "changelog-max-age", time.Duration(7200)*time.Second))
				}
			}
		case "changelog-max-age":
			d := ctx.Duration(flag)
			if d < 0 {
				return fmt.Errorf("negative duration for %s: %s", flag, d)
			}
			newAge := int64(d.Seconds())
			if newAge != format.ChangeLogMaxAge {
				msg.WriteString(fmt.Sprintf("%10s: %s -> %s\n", flag, time.Duration(format.ChangeLogMaxAge)*time.Second, d))
				format.ChangeLogMaxAge = newAge
			}
		case "changelog-max-lines":
			if new := ctx.Int64(flag); new != format.ChangeLogMaxLines {
				if new < 0 {
					return fmt.Errorf("negative value for %s: %d", flag, new)
				}
				msg.WriteString(fmt.Sprintf("%10s: %d -> %d\n", flag, format.ChangeLogMaxLines, new))
				format.ChangeLogMaxLines = new
			}
		case "user-group-quota":
			if new := ctx.Bool(flag); new != format.UserGroupQuota {
				msg.WriteString(fmt.Sprintf("%10s: %t -> %t\n", flag, format.UserGroupQuota, new))
				format.UserGroupQuota = new
			}
		case "min-client-version":
			if new := ctx.String(flag); new != format.MinClientVersion {
				if version.Parse(new) == nil {
					return fmt.Errorf("Invalid version string: %s", new)
				}
				if compareVersion(new, format.MinClientVersion) < 0 {
					return fmt.Errorf("cannot lower min-client-version from %s to %s", format.MinClientVersion, new)
				}
				requireMinClientVersion(new)
			}
		case "max-client-version":
			if new := ctx.String(flag); new != format.MaxClientVersion {
				if version.Parse(new) == nil {
					return fmt.Errorf("Invalid version string: %s", new)
				}
				msg.WriteString(fmt.Sprintf("%s: %s -> %s\n", flag, format.MaxClientVersion, new))
				format.MaxClientVersion = new
				clientVer = true
			}
		case "enable-acl":
			if enableACL := ctx.Bool(flag); enableACL != format.EnableACL {
				if enableACL {
					msg.WriteString(fmt.Sprintf("%s: %v -> %v\n", flag, format.EnableACL, true))
					format.EnableACL = true
					requireMinClientVersion("1.2.0-A")
				} else {
					return errors.New("cannot disable acl")
				}
			}
		case "ranger-rest-url":
			if newUrl := ctx.String(flag); newUrl != format.RangerRestUrl {
				msg.WriteString(fmt.Sprintf("%s: %s -> %s\n", flag, format.RangerRestUrl, newUrl))
				format.RangerRestUrl = newUrl
				requireMinClientVersion("1.3.0-A")
			}
		case "ranger-service":
			if newService := ctx.String(flag); newService != format.RangerService {
				msg.WriteString(fmt.Sprintf("%s: %s -> %s\n", flag, format.RangerService, newService))
				format.RangerService = newService
				requireMinClientVersion("1.3.0-A")
			}
		case "kerberos-config-file":
			msg.WriteString(fmt.Sprintf("%s: updated\n", flag))
			format.KerbConf = readKerbConf(ctx.String(flag))
			requireMinClientVersion("1.4.0-A")
		case "tier":
			if ctx.IsSet("bucket") || ctx.IsSet("access-key") || ctx.IsSet("secret-key") || ctx.IsSet("session-token") {
				logger.Fatalf("Current tiered storage does not support multi-bucket mode")
			}
			requireMinClientVersion("1.4.0-A")
			targetTierID = uint8(ctx.Int(flag))
			currentTier, findTier = format.Tiers[targetTierID]
			if !findTier {
				currentTier = object.Tier{ID: targetTierID}
			}
			newTier = object.Tier{
				ID:  targetTierID,
				Sc:  currentTier.Sc,
				Tag: currentTier.Tag,
			}
			var change bool
			if ctx.IsSet("storage-class") {
				change = true
				newTier.Sc = ctx.String("storage-class")
				if !findTier || currentTier.Sc != newTier.Sc {
					msg.WriteString(fmt.Sprintf("set tier %d storage-class: %s\n", targetTierID, newTier.Sc))
					tier = true
				}
			}

			if ctx.IsSet("tag") {
				change = true
				newTier.Tag = ctx.String("tag")
				if !object.ValidateTag(newTier.Tag) {
					logger.Fatalf("Invalid tag format: %s", newTier.Tag)
				}
				if !findTier || currentTier.Tag != newTier.Tag {
					msg.WriteString(fmt.Sprintf("set tier %d tag: %s\n", targetTierID, newTier.Tag))
					tier = true
				}
			}
			if change {
				format.Tiers[targetTierID] = newTier
			} else {
				logger.Fatalf("missing required flag: --storage-class or --tag")
			}
		}
	}

	if compareVersion(format.MinClientVersion, requiredMinClientVersion) < 0 {
		msg.WriteString("min-client-version: ")
		msg.WriteString(format.MinClientVersion)
		msg.WriteString(" -> ")
		msg.WriteString(requiredMinClientVersion)
		msg.WriteByte('\n')
		format.MinClientVersion = requiredMinClientVersion
		clientVer = true
	}

	if msg.Len() == 0 {
		fmt.Println("Nothing changed.")
		return nil
	}

	if !ctx.Bool("force") {
		yes := ctx.Bool("yes")
		if storage || tier {
			blob, err := createStorage(*format)
			if err != nil {
				return err
			}
			if err = test(context.WithValue(context.Background(), object.TierKey{}, targetTierID), blob); err != nil {
				return err
			}
		}
		if quota {
			var totalSpace, availSpace, iused, iavail uint64
			_ = m.StatFS(meta.Background(), meta.RootInode, &totalSpace, &availSpace, &iused, &iavail)
			usedSpace := totalSpace - availSpace
			if format.Capacity > 0 && usedSpace >= format.Capacity ||
				format.Inodes > 0 && iused >= format.Inodes {
				warn("New quota is too small (used / quota): %d / %d bytes, %d / %d inodes.",
					usedSpace, format.Capacity, iused, format.Inodes)
				if !yes && !userConfirmed() {
					return fmt.Errorf("Aborted.")
				}
			}
		}
		if trash && format.TrashDays == 0 {
			warn("The current trash will be emptied and future removed files will purged immediately.")
			if !yes && !userConfirmed() {
				return fmt.Errorf("Aborted.")
			}
		}
		if originDirStats && !format.DirStats {
			qs := make(map[string]*meta.Quota)
			err := m.HandleQuota(meta.Background(), meta.QuotaList, "", meta.DirQuotaType, qs, false, false, false)
			if err != nil {
				return errors.Wrap(err, "list quotas")
			}
			if len(qs) != 0 {
				paths := make([]string, 0, len(qs))
				for path := range qs {
					paths = append(paths, path)
				}
				return fmt.Errorf("cannot disable dir stats when there are still %d dir quotas: %v", len(qs), paths)
			}
		}
		if clientVer {
			confirmClientVer := false
			if format.CheckVersion() != nil {
				warn("Clients below version %s will be rejected after modification.", format.MinClientVersion)
				confirmClientVer = true
			}

			// check all clients
			if sessions, err := m.ListSessions(); err == nil {
				warnMsg := ""
				for _, session := range sessions {
					if err := format.CheckCliVersion(version.Parse(session.Version)); err != nil {
						warnMsg += fmt.Sprintf("host %s pid %d client version error: %s\n", session.HostName, session.ProcessID, err)
					}
				}
				if warnMsg != "" {
					fmt.Println(warnMsg)
					confirmClientVer = true
				}
			}
			if confirmClientVer && !yes && !userConfirmed() {
				return fmt.Errorf("Aborted.")
			}
		}
		if tier {
			if findTier && (currentTier.Sc != newTier.Sc || currentTier.Tag != newTier.Tag) {
				fmt.Printf("existing tier will be overwritten: \n")
				fmt.Printf("tier: %d\n", currentTier.ID)
				if ctx.IsSet("storage-class") && currentTier.Sc != newTier.Sc {
					fmt.Printf("storage class: %s => %s\n", currentTier.Sc, newTier.Sc)
				}
				if ctx.IsSet("tag") && currentTier.Tag != newTier.Tag {
					fmt.Printf("tag: %s => %s\n", currentTier.Tag, newTier.Tag)
				}
				if !yes && !userConfirmed() {
					return fmt.Errorf("Aborted.")
				}
			}
		}
	}

	if encrypted || ctx.Bool("encrypt-secret") {
		if err = format.Encrypt(); err != nil {
			logger.Fatalf("Format encrypt: %s", err)
		}
	}
	if err = m.Init(format, false); err == nil {
		fmt.Println(msg.String()[:msg.Len()-1])
	}

	if !originUGQuota && format.UserGroupQuota {
		if err = m.ScanUserGroupUsage(meta.Background()); err != nil {
			logger.Warnf("Scan user group usage: %s", err)
		}
	}

	return err
}
