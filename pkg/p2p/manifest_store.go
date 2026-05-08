package p2p

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"
)

// uploadManifest builds a Manifest for the given paths/blocks and uploads it
// to object storage at ManifestKey(...). Used by the leader peer.
//
// Before uploading, the existing object (if any) is validated against this
// run's paths/config:
//   - matches → upload skipped (idempotent re-run / crash recovery).
//   - mismatches → refuse to overwrite; another warmup is using the same
//     --manifest-name and would lose its manifest.
//   - corrupt/unparseable → log warning and overwrite (best-effort recovery).
//   - missing or transient Get error → proceed with the upload.
func (w *Warmup) uploadManifest(ctx context.Context, paths []string, blocks []*Block) error {
	if w.storage == nil {
		return fmt.Errorf("uploadManifest: nil object storage")
	}
	manifest := BuildManifest(paths, w.config.BlockSize, w.config.HashPrefix, blocks)
	key := ManifestKey(paths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)

	if existing, err := w.fetchManifestBytes(ctx, key); err == nil {
		old, perr := UnmarshalManifest(existing)
		if perr != nil {
			logger.Warnf("manifest at %q exists but is not parseable; overwriting (%v)", key, perr)
		} else if verr := old.Validate(paths, w.config.BlockSize, w.config.HashPrefix); verr == nil {
			logger.Infof("manifest at %q already matches our content; skipping upload", key)
			return nil
		} else {
			hint := ""
			if w.config.ManifestName != "" {
				hint = fmt.Sprintf(" — another warmup is using --manifest-name=%q. Pick a different name or delete the existing object.", w.config.ManifestName)
			}
			return fmt.Errorf("refusing to overwrite manifest %q (existing %s)%s", key, verr, hint)
		}
	}

	data, err := manifest.Marshal()
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := w.storage.Put(ctx, key, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("put manifest %q: %w", key, err)
	}
	return nil
}

// downloadManifest polls object storage for a manifest matching paths and the
// current Warmup config, retrying every pollInterval until either the manifest
// arrives or timeout elapses. Used by follower peers waiting for the leader's
// upload. Any storage error is treated as transient (retry until timeout); a
// permanent storage failure surfaces as a timeout error.
func (w *Warmup) downloadManifest(ctx context.Context, paths []string, timeout, pollInterval time.Duration) (*Manifest, error) {
	if w.storage == nil {
		return nil, fmt.Errorf("downloadManifest: nil object storage")
	}
	key := ManifestKey(paths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		data, err := w.fetchManifestBytes(ctx, key)
		if err == nil {
			return UnmarshalManifest(data)
		}
		lastErr = err

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("manifest %q not available after %v: %w", key, timeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

func (w *Warmup) fetchManifestBytes(ctx context.Context, key string) ([]byte, error) {
	rc, err := w.storage.Get(ctx, key, 0, -1)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// tryFetchValidManifest does a single Get on the manifest object and returns
// the parsed manifest only when it exists, decodes correctly, and matches our
// paths/blocksize/hashprefix. Any failure returns nil so the caller can fall
// through to the canonical leader/follower path.
//
// Used as a fast-path at the start of resolveOrDownloadManifest so that a peer
// joining a warmed cluster — including one that incorrectly elects itself
// leader because it happens to have the smallest address — reuses the existing
// manifest instead of triggering a redundant meta-engine scan.
func (w *Warmup) tryFetchValidManifest(ctx context.Context, paths []string) *Manifest {
	if w.storage == nil {
		return nil
	}
	key := ManifestKey(paths, w.config.BlockSize, w.config.HashPrefix, w.config.ManifestName)
	data, err := w.fetchManifestBytes(ctx, key)
	if err != nil {
		return nil
	}
	m, perr := UnmarshalManifest(data)
	if perr != nil {
		logger.WithError(perr).Warnf("manifest at %q exists but is unparseable; ignoring", key)
		return nil
	}
	if verr := m.Validate(paths, w.config.BlockSize, w.config.HashPrefix); verr != nil {
		logger.WithError(verr).Warnf("manifest at %q exists but mismatches our config; ignoring", key)
		return nil
	}
	return m
}
