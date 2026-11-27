/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package object

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/stretchr/testify/require"
)

type passthroughWrapper struct {
	ObjectStorage
}

func (p *passthroughWrapper) Unwrap() ObjectStorage {
	return p.ObjectStorage
}

type fullCapStub struct {
	*filestore
	tierStorage
	shutdowns   int
	streamKeys  []string
	streamBytes []byte
}

func (s *fullCapStub) Shutdown() {
	s.shutdowns++
}

func (s *fullCapStub) UploadPartStream(key string, uploadID string, num int, in io.Reader) (*Part, error) {
	data, err := io.ReadAll(in)
	if err != nil {
		return nil, err
	}
	s.streamKeys = append(s.streamKeys, key)
	s.streamBytes = data
	return &Part{Num: num, Size: len(data)}, nil
}

type overridingWrapper struct {
	passthroughWrapper
	calls []string
}

func (w *overridingWrapper) Chtimes(key string, mtime time.Time) error {
	w.calls = append(w.calls, "chtimes:"+key)
	return nil
}

func (w *overridingWrapper) Symlink(oldName, newName string) error {
	w.calls = append(w.calls, "symlink:"+oldName+"->"+newName)
	return nil
}

func (w *overridingWrapper) Readlink(name string) (string, error) {
	w.calls = append(w.calls, "readlink:"+name)
	return "target", nil
}

func (w *overridingWrapper) UploadPartStream(key string, uploadID string, num int, in io.Reader) (*Part, error) {
	w.calls = append(w.calls, "stream:"+key)
	return &Part{Num: num}, nil
}

func TestStorageControllerCapabilities(t *testing.T) {
	ctx := context.Background()
	enc, err := NewDataEncryptor(NewRSAEncryptor(rsaKey), AES256GCM_RSA)
	require.NoError(t, err)

	t.Run("nil storage", func(t *testing.T) {
		sc := NewStorageController(nil)
		sc.Shutdown()
		require.False(t, sc.IsFileSystem())
		require.False(t, sc.SupportSymlink())
		require.ErrorIs(t, sc.InitTiers(NewTiers("")), utils.ErrNotSUP)
		require.Equal(t, Tier{}, sc.GetTier(ctx))
		require.ErrorIs(t, sc.Chmod("k", 0600), utils.ErrNotSUP)
		require.ErrorIs(t, sc.Chown("k", "u", "g"), utils.ErrNotSUP)
		require.ErrorIs(t, sc.Chtimes("k", time.Now()), utils.ErrNotSUP)
		require.ErrorIs(t, sc.Symlink("old", "new"), utils.ErrNotSUP)
		_, err := sc.Readlink("new")
		require.ErrorIs(t, err, utils.ErrNotSUP)
		_, err = sc.UploadPartStream("k", "u", 1, bytes.NewReader(nil))
		require.ErrorIs(t, err, utils.ErrNotSUP)
	})

	t.Run("non-filesystem wrapped with prefix and chunked encryption", func(t *testing.T) {
		mem, err := CreateStorage("mem", "", "", "", "")
		require.NoError(t, err)
		wrapped := NewChunkedEncrypted(WithPrefix(&passthroughWrapper{mem}, "p/"), enc)

		sc := NewStorageController(wrapped)
		require.False(t, sc.IsFileSystem())
		require.False(t, IsFileSystem(wrapped))
		require.False(t, sc.SupportSymlink())
		require.ErrorIs(t, sc.Chmod("a", 0644), utils.ErrNotSUP)
		require.ErrorIs(t, sc.Symlink("x", "y"), utils.ErrNotSUP)
	})

	t.Run("filesystem prefix accumulation and wrapper order independence", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			wrap func(ObjectStorage) ObjectStorage
		}{
			{
				name: "prefix_outside_passthrough",
				wrap: func(s ObjectStorage) ObjectStorage {
					return WithPrefix(&passthroughWrapper{WithPrefix(s, "a/")}, "b/")
				},
			},
			{
				name: "passthrough_outside_chunked_encrypted_and_prefix",
				wrap: func(s ObjectStorage) ObjectStorage {
					return &passthroughWrapper{NewChunkedEncrypted(WithPrefix(WithPrefix(s, "a/"), "b/"), enc)}
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				root := t.TempDir()
				fsStore, err := CreateStorage("file", root+"/", "", "", "")
				require.NoError(t, err)
				stub := &fullCapStub{filestore: fsStore.(*filestore)}
				s := tc.wrap(stub)

				sc := NewStorageController(s)
				require.True(t, sc.IsFileSystem())
				require.True(t, IsFileSystem(s))
				require.True(t, sc.SupportSymlink())

				require.NoError(t, s.Put(ctx, "sub/file.txt", bytes.NewReader([]byte("hello"))))
				realPath := filepath.Join(root, "a", "b", "sub", "file.txt")
				require.FileExists(t, realPath)

				require.NoError(t, sc.Chmod("sub/file.txt", 0600))
				info, err := os.Stat(realPath)
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0600), info.Mode().Perm())

				mtime := time.Unix(1700000000, 0)
				require.NoError(t, sc.Chtimes("sub/file.txt", mtime))
				info, err = os.Stat(realPath)
				require.NoError(t, err)
				require.Equal(t, mtime.Unix(), info.ModTime().Unix())

				require.NoError(t, sc.Symlink("file.txt", "sub/link.txt"))
				target, err := sc.Readlink("sub/link.txt")
				require.NoError(t, err)
				require.Equal(t, "file.txt", target)
				rawTarget, err := os.Readlink(filepath.Join(root, "a", "b", "sub", "link.txt"))
				require.NoError(t, err)
				require.Equal(t, "file.txt", rawTarget)

				Shutdown(s)
				require.Equal(t, 1, stub.shutdowns)
			})
		}
	})

	t.Run("whole-object encrypted blocks namespace and stream but keeps shutdown and tiers", func(t *testing.T) {
		root := t.TempDir()
		fsStore, err := CreateStorage("file", root+"/", "", "", "")
		require.NoError(t, err)
		stub := &fullCapStub{filestore: fsStore.(*filestore)}
		s := NewEncrypted(WithPrefix(&passthroughWrapper{stub}, "vol/"), enc)

		sc := NewStorageController(s)
		require.False(t, sc.IsFileSystem())
		require.False(t, sc.SupportSymlink())
		_, err = sc.UploadPartStream("k", "u", 1, bytes.NewReader([]byte("plain")))
		require.ErrorIs(t, err, utils.ErrNotSUP)

		tiers := NewTiers("STANDARD_IA")
		require.NoError(t, sc.InitTiers(tiers))
		require.Equal(t, "STANDARD_IA", sc.GetTier(ctx).Sc)

		Shutdown(s)
		require.Equal(t, 1, stub.shutdowns)
	})

	t.Run("upload part stream prefixes through passthrough and blocks chunked encryption", func(t *testing.T) {
		fsStore, err := CreateStorage("file", t.TempDir()+"/", "", "", "")
		require.NoError(t, err)
		stub := &fullCapStub{filestore: fsStore.(*filestore)}

		prefixed := &passthroughWrapper{WithPrefix(stub, "dir/")}
		part, err := NewStorageController(prefixed).UploadPartStream("obj", "uid", 2, bytes.NewReader([]byte("payload")))
		require.NoError(t, err)
		require.Equal(t, 2, part.Num)
		require.Equal(t, []string{"dir/obj"}, stub.streamKeys)
		require.Equal(t, []byte("payload"), stub.streamBytes)

		encStore := NewChunkedEncrypted(prefixed, enc)
		_, err = NewStorageController(encStore).UploadPartStream("obj", "uid", 1, bytes.NewReader([]byte("secret")))
		require.ErrorIs(t, err, utils.ErrNotSUP)
	})

	t.Run("sharded shutdown and tiers", func(t *testing.T) {
		fs1, _ := CreateStorage("file", t.TempDir()+"/", "", "", "")
		fs2, _ := CreateStorage("file", t.TempDir()+"/", "", "", "")
		s1 := &fullCapStub{filestore: fs1.(*filestore)}
		s2 := &fullCapStub{filestore: fs2.(*filestore)}
		sh := WithPrefix(&sharded{stores: []ObjectStorage{&passthroughWrapper{s1}, s2}}, "vol/")

		sc := NewStorageController(sh)
		require.NoError(t, sc.InitTiers(NewTiers("GLACIER")))
		require.Equal(t, "GLACIER", sc.GetTier(ctx).Sc)

		Shutdown(sh)
		require.Equal(t, 1, s1.shutdowns)
		require.Equal(t, 1, s2.shutdowns)
	})

	t.Run("wrapper's own capability takes precedence over unwrap", func(t *testing.T) {
		mem, err := CreateStorage("mem", "", "", "", "")
		require.NoError(t, err)
		w := &overridingWrapper{passthroughWrapper: passthroughWrapper{mem}}

		sc := NewStorageController(WithPrefix(w, "p/"))
		require.False(t, sc.IsFileSystem())
		require.True(t, sc.SupportSymlink())
		require.NoError(t, sc.Chtimes("k", time.Now()))
		require.NoError(t, sc.Symlink("old", "l"))
		target, err := sc.Readlink("l")
		require.NoError(t, err)
		require.Equal(t, "target", target)
		_, err = sc.UploadPartStream("k", "u", 1, bytes.NewReader(nil))
		require.NoError(t, err)
		require.Equal(t, []string{"chtimes:p/k", "symlink:old->p/l", "readlink:p/l", "stream:p/k"}, w.calls)

		w.calls = nil
		encSC := NewStorageController(NewChunkedEncrypted(w, enc))
		require.NoError(t, encSC.Chtimes("k", time.Now()))
		_, err = encSC.UploadPartStream("k", "u", 1, bytes.NewReader(nil))
		require.ErrorIs(t, err, utils.ErrNotSUP)
		require.Equal(t, []string{"chtimes:k"}, w.calls)
	})
}
