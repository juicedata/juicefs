/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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
	"os"
	"os/signal"
	"path"
	"runtime"
	"strings"
	"syscall"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/p2p"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdP2PWarmup() *cli.Command {
	return &cli.Command{
		Name:      "p2p-warmup",
		Action:    p2pWarmup,
		Category:  "TOOL",
		Usage:     "Build cache using peer-to-peer block exchange",
		ArgsUsage: "META-URL PATH ...",
		Description: `Warm up cache across multiple nodes using P2P block exchange.
Each node fetches different blocks from object storage and shares them with peers,
reducing object storage load to 1/N across N peers.

Examples:
  juicefs p2p-warmup --peers-dns-a my-svc.ns.svc.cluster.local redis://redis:6379/1 /models/llama
  juicefs p2p-warmup --peers-list 10.0.0.1:19090,10.0.0.2:19090 tikv://tikv:2379 /data/dataset`,
		Flags: []cli.Flag{
			// P2P flags
			&cli.StringFlag{Name: "peers-dns-srv", Usage: "DNS SRV record for peer discovery"},
			&cli.StringFlag{Name: "peers-dns-a", Usage: "DNS A record FQDN for peer discovery"},
			&cli.StringFlag{Name: "peers-list", Usage: "static peer list, comma-separated"},
			&cli.StringFlag{Name: "listen", Value: ":19090", Usage: "HTTP server listen address"},
			&cli.StringFlag{Name: "discovery-interval", Value: "3s", Usage: "peer discovery polling interval"},
			&cli.StringFlag{Name: "availability-interval", Value: "2s", Usage: "availability polling interval"},
			&cli.IntFlag{Name: "min-peers", Usage: "minimum total peer count including self; wait until at least this many are discovered before starting. Discovery returning more proceeds immediately (lower bound, not fixed size). >=2 enables manifest sharing. 0 or 1 = solo mode."},
			&cli.BoolFlag{Name: "disable-manifest-sharing", Usage: "force every peer to resolve metadata independently (escape hatch for debugging)"},
			&cli.StringFlag{Name: "leader-timeout", Value: "10m", Usage: "follower max wait for the leader's manifest before erroring out"},
			&cli.StringFlag{Name: "manifest-name", Usage: "manifest object basename under p2p_warmup/ (default: content-addressable hash). All peers must pass the same value to agree on the key."},
			&cli.BoolFlag{Name: "delete-manifest", Usage: "delete the manifest matching the other flags (paths, --manifest-name) from object storage and exit without warming"},
			// General flags
			&cli.IntFlag{Name: "threads", Aliases: []string{"p"}, Value: 50, Usage: "concurrent fetch workers"},
			&cli.StringFlag{Name: "file", Aliases: []string{"f"}, Usage: "file containing a list of paths"},
			&cli.BoolFlag{Name: "keep-alive", Usage: "keep serving blocks to peers after warmup completes (until SIGTERM/SIGINT)"},
			&cli.StringFlag{Name: "keep-alive-timeout", Value: "0", Usage: "auto-shutdown after this duration in keep-alive mode (0=wait for SIGTERM); ignored without --keep-alive"},
			&cli.StringFlag{Name: "cache-dir", Usage: "cache directory (default: from volume format)"},
			&cli.StringFlag{Name: "cache-size", Value: "100G", Usage: "hard cap on total cached bytes at cache-dir (e.g. 100G, 50000 = 50000 MiB); 0 disables the cap. Counts pre-existing files. Matches mount's --cache-size default."},
			&cli.StringFlag{Name: "download-limit", Usage: "bandwidth limit for download from object storage in Mbps per peer (e.g. 800 or 1G); peer-to-peer transfers are not throttled"},
		},
	}
}

func p2pWarmup(c *cli.Context) error {
	setup0(c, 2, 0) // at least META-URL + one PATH, unlimited max

	addr := c.Args().Get(0)
	removePassword(addr)

	// Collect paths from positional args and --file flag.
	paths := c.Args().Tail()
	if fname := c.String("file"); fname != "" {
		fd, err := os.Open(fname)
		if err != nil {
			logger.Fatalf("Failed to open file %s: %s", fname, err)
		}
		defer fd.Close()
		scanner := bufio.NewScanner(fd)
		for scanner.Scan() {
			if p := strings.TrimSpace(scanner.Text()); p != "" {
				paths = append(paths, p)
			}
		}
		if err = scanner.Err(); err != nil {
			logger.Fatalf("Reading file %s failed with error: %s", fname, err)
		}
	}
	if len(paths) == 0 {
		logger.Infof("no paths specified")
		return nil
	}

	// Initialize meta client.
	metaConf := meta.DefaultConf()
	metaConf.ReadOnly = true
	metaConf.NoBGJob = true
	metaCli := meta.NewClient(addr, metaConf)
	format, err := metaCli.Load(true)
	if err != nil {
		return err
	}

	// Create object storage from format.
	blob, err := createStorage(*format)
	if err != nil {
		return err
	}

	// Maintenance mode: delete the manifest matching these args and exit.
	// Done before any warmup wiring so a typo doesn't accidentally start
	// the heavy machinery.
	if c.Bool("delete-manifest") {
		name := strings.TrimSpace(c.String("manifest-name"))
		key, err := p2p.DeleteManifest(context.Background(), blob, paths, format.BlockSize*1024, format.HashPrefix, name)
		if err != nil {
			return err
		}
		logger.Infof("deleted manifest %q", key)
		return nil
	}

	// Determine cache directory.
	cacheDir := c.String("cache-dir")
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	// Append volume UUID to cache dir, same as chunk.Config.SelfCheck().
	cacheDir = path.Join(cacheDir, format.UUID)

	// Build WarmupConfig from CLI flags.
	config := p2p.WarmupConfig{
		ListenAddr:           c.String("listen"),
		DiscoveryInterval:    utils.Duration(c.String("discovery-interval")),
		AvailabilityInterval: utils.Duration(c.String("availability-interval")),
		Threads:              c.Int("threads"),
		KeepAlive:            c.Bool("keep-alive"),
		KeepAliveTimeout:     utils.Duration(c.String("keep-alive-timeout")),
		MinPeers:             c.Int("min-peers"),

		DisableManifestSharing: c.Bool("disable-manifest-sharing"),
		LeaderTimeout:          utils.Duration(c.String("leader-timeout")),
		ManifestName:           strings.TrimSpace(c.String("manifest-name")),

		PeersDNSSRV: c.String("peers-dns-srv"),
		PeersDNSA:   c.String("peers-dns-a"),
		PeersList:   c.String("peers-list"),

		CacheDir:   cacheDir,
		BlockSize:  format.BlockSize * 1024,
		HashPrefix: format.HashPrefix,
		Compress:   format.Compression,

		DownloadLimit: utils.ParseMbps(c, "download-limit") * 1e6 / 8,
		CacheSize:     int64(utils.ParseBytes(c, "cache-size", 'M')),
	}

	// Set up context with signal cancellation.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	return p2p.NewWarmup(config, metaCli, blob).Run(ctx, paths)
}

// defaultCacheDir returns the platform-appropriate default cache directory,
// matching the logic in dataCacheFlags().
func defaultCacheDir() string {
	dir := "/var/jfsCache"
	switch runtime.GOOS {
	case "linux":
		if os.Getuid() == 0 {
			return dir
		}
		fallthrough
	case "darwin":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return dir
		}
		dir = path.Join(homeDir, ".juicefs", "cache")
	case "windows":
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return dir
		}
		dir = path.Join(homeDir, ".juicefs", "cache")
	}
	return dir
}
