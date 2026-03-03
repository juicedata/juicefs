package vfs

import (
	"syscall"
	"testing"

	"github.com/juicedata/juicefs/pkg/meta"
)

func TestPathProtectionBasic(t *testing.T) {
	// Test parsing path protection config from JSON
	// Pattern matches full path with ^ anchor
	jsonStr := `{"rules":[{"pattern":"^/mnt/test/data/.*\\.git.*", "mode": "readonly"}]}`
	config, err := ParsePathProtectionConfig(jsonStr)
	if err != nil {
		t.Fatalf("Error parsing path protection config: %v", err)
	}

	if len(config.Rules) == 0 {
		t.Fatalf("No rules provided")
	}

	if !config.Enabled {
		t.Fatal("Config should be enabled when rules are present")
	}

	// Initialize path protector
	pp, err := NewPathProtector(config, "/mnt/test")
	if err != nil {
		t.Fatalf("Error creating path protector: %v", err)
	}

	// Test write protection on matched path
	errno := pp.CheckWrite("/mnt/test/data/project/.git/config")
	if errno != 1 { // syscall.EPERM = 1
		t.Errorf("Expected write to be blocked for .git path, got errno=%d", errno)
	}

	// Test write allowed on non-protected path
	errno = pp.CheckWrite("/mnt/test/data/project/src/main.go")
	if errno != 0 {
		t.Errorf("Expected write to be allowed for non-protected path, got errno=%d", errno)
	}

	// Test read allowed on readonly path (should succeed)
	errno = pp.CheckRead("/mnt/test/data/project/.git/config")
	if errno != 0 {
		t.Errorf("Expected read to be allowed for readonly path, got errno=%d", errno)
	}
}

func TestPathProtectionDenyMode(t *testing.T) {
	jsonStr := `{"rules":[{"pattern":"^/mnt/test/data/config/.*", "mode": "deny"}]}`
	config, err := ParsePathProtectionConfig(jsonStr)
	if err != nil {
		t.Fatalf("Error parsing path protection config: %v", err)
	}

	pp, err := NewPathProtector(config, "/mnt/test")
	if err != nil {
		t.Fatalf("Error creating path protector: %v", err)
	}

	// Test write blocked
	errno := pp.CheckWrite("/mnt/test/data/config/settings.json")
	if errno == 0 {
		t.Error("Expected write to be blocked in deny mode")
	}

	// Test read blocked
	errno = pp.CheckRead("/mnt/test/data/config/settings.json")
	if errno == 0 {
		t.Error("Expected read to be blocked in deny mode")
	}
}

func TestPathProtectionEmpty(t *testing.T) {
	// Test empty config
	config, err := ParsePathProtectionConfig("")
	if err != nil {
		t.Fatalf("Error parsing empty config: %v", err)
	}

	if config.Enabled {
		t.Error("Empty config should not be enabled")
	}

	pp, err := NewPathProtector(config, "/mnt/test")
	if err != nil {
		t.Fatalf("Error creating path protector: %v", err)
	}

	// All operations should be allowed with empty config
	if errno := pp.CheckWrite("/mnt/test/any/path"); errno != 0 {
		t.Errorf("Expected write to be allowed with empty config, got errno=%d", errno)
	}
	if errno := pp.CheckRead("/mnt/test/any/path"); errno != 0 {
		t.Errorf("Expected read to be allowed with empty config, got errno=%d", errno)
	}
}

func TestPathProtectionNil(t *testing.T) {
	// Test nil protector
	var pp *PathProtector

	if errno := pp.CheckWrite("/any/path"); errno != 0 {
		t.Errorf("Nil protector should allow all writes, got errno=%d", errno)
	}
	if errno := pp.CheckRead("/any/path"); errno != 0 {
		t.Errorf("Nil protector should allow all reads, got errno=%d", errno)
	}
	if pp.IsProtected("/any/path") {
		t.Error("Nil protector should not report any path as protected")
	}
}

func TestPathProtectionMultipleRules(t *testing.T) {
	jsonStr := `{"rules":[
		{"pattern":"^/mnt/data/.*\\.git.*", "mode": "readonly"},
		{"pattern":"^/mnt/data/secrets/.*", "mode": "deny"},
		{"pattern":"^/mnt/tmp/.*", "mode": "readonly"}
	]}`
	config, err := ParsePathProtectionConfig(jsonStr)
	if err != nil {
		t.Fatalf("Error parsing config: %v", err)
	}

	pp, err := NewPathProtector(config, "/mnt")
	if err != nil {
		t.Fatalf("Error creating path protector: %v", err)
	}

	// Test .git path (readonly)
	if !pp.IsProtected("/mnt/data/project/.git") {
		t.Error(".git path should be protected")
	}
	if errno := pp.CheckRead("/mnt/data/project/.git/config"); errno != 0 {
		t.Error("readonly path should allow reads")
	}

	// Test secrets path (deny)
	if errno := pp.CheckRead("/mnt/data/secrets/api_key.txt"); errno == 0 {
		t.Error("deny path should block reads")
	}

	// Test tmp path (readonly)
	if errno := pp.CheckWrite("/mnt/tmp/test.txt"); errno == 0 {
		t.Error("readonly path should block writes")
	}
}

func TestGetProtectionMode(t *testing.T) {
	jsonStr := `{"rules":[{"pattern":"^/mnt/data/.*\\.git.*", "mode": "readonly"}]}`
	config, _ := ParsePathProtectionConfig(jsonStr)

	pp, _ := NewPathProtector(config, "/mnt")

	mode := pp.GetProtectionMode("/mnt/data/project/.git")
	if mode != ProtectionModeReadonly {
		t.Errorf("Expected readonly mode, got %s", mode)
	}

	mode = pp.GetProtectionMode("/mnt/data/project/src")
	if mode != "" {
		t.Errorf("Expected empty mode for unprotected path, got %s", mode)
	}
}

func TestGetStats(t *testing.T) {
	jsonStr := `{"rules":[{"pattern":"^/mnt/test/data/.*\\.git.*", "mode": "readonly"}]}`
	config, _ := ParsePathProtectionConfig(jsonStr)

	pp, _ := NewPathProtector(config, "/mnt/test")
	stats := pp.GetStats()

	if !stats["enabled"].(bool) {
		t.Error("Stats should show enabled=true")
	}
	if stats["mountpoint"].(string) != "/mnt/test" {
		t.Errorf("Expected mountpoint /mnt/test, got %s", stats["mountpoint"])
	}
}

// TestReadProtectionWithDenyMode tests that Read method properly blocks reads in deny mode
func TestReadProtectionWithDenyMode(t *testing.T) {
	// Create VFS without path protection initially
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.NewContext(10, 1, []uint32{2, 3}))

	// First, create directory and file structure without protection
	dataDir, errno := v.Mkdir(ctx, 1, "data", 0755, 0)
	if errno != 0 {
		t.Fatalf("Failed to create /data directory: %v", errno)
	}

	secretsDir, errno := v.Mkdir(ctx, dataDir.Inode, "secrets", 0755, 0)
	if errno != 0 {
		t.Fatalf("Failed to create /data/secrets directory: %v", errno)
	}

	// Create a file in the protected directory
	fe, fh, errno := v.Create(ctx, secretsDir.Inode, "secret.txt", 0644, 0, syscall.O_RDWR)
	if errno != 0 {
		t.Fatalf("Failed to create file: %v", errno)
	}

	// Write some data to the file
	data := []byte("secret data")
	errno = v.Write(ctx, fe.Inode, data, 0, fh)
	if errno != 0 {
		t.Fatalf("Failed to write to file: %v", errno)
	}

	// Now enable path protection with deny mode
	jsonStr := `{"rules":[{"pattern":"^/jfs/data/secrets/.*", "mode": "deny"}]}`
	config, err := ParsePathProtectionConfig(jsonStr)
	if err != nil {
		t.Fatalf("Error parsing config: %v", err)
	}

	pp, err := NewPathProtector(config, "/jfs")
	if err != nil {
		t.Fatalf("Error creating path protector: %v", err)
	}
	v.PathProtector = pp

	// Now try to read from the protected file - should be blocked
	buf := make([]byte, 100)
	_, errno = v.Read(ctx, fe.Inode, buf, 0, fh)
	if errno != syscall.EACCES {
		t.Errorf("Expected read to be blocked with EACCES, got errno=%d", errno)
	}

	// Create a file in unprotected path (before protection was enabled)
	fe2, fh2, errno := v.Create(ctx, 1, "public.txt", 0644, 0, syscall.O_RDWR)
	if errno != 0 {
		t.Fatalf("Failed to create public file: %v", errno)
	}

	// Write some data
	errno = v.Write(ctx, fe2.Inode, []byte("public data"), 0, fh2)
	if errno != 0 {
		t.Fatalf("Failed to write to public file: %v", errno)
	}

	// Read from unprotected file - should succeed
	_, errno = v.Read(ctx, fe2.Inode, buf, 0, fh2)
	if errno != 0 {
		t.Errorf("Expected read to succeed for unprotected path, got errno=%d", errno)
	}
}

// TestLookupProtectionWithDenyMode tests that Lookup method properly blocks lookups in deny mode
func TestLookupProtectionWithDenyMode(t *testing.T) {
	// Create VFS without path protection initially
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.NewContext(10, 1, []uint32{2, 3}))

	// First, create directory and file structure without protection
	dataDir, errno := v.Mkdir(ctx, 1, "data", 0755, 0)
	if errno != 0 {
		t.Fatalf("Failed to create /data directory: %v", errno)
	}

	secretsDir, errno := v.Mkdir(ctx, dataDir.Inode, "secrets", 0755, 0)
	if errno != 0 {
		t.Fatalf("Failed to create /data/secrets directory: %v", errno)
	}

	// Create a file in the protected directory
	_, _, errno = v.Create(ctx, secretsDir.Inode, "secret.txt", 0644, 0, syscall.O_RDWR)
	if errno != 0 {
		t.Fatalf("Failed to create file: %v", errno)
	}

	// Now enable path protection with deny mode
	jsonStr := `{"rules":[{"pattern":"^/jfs/data/secrets/.*", "mode": "deny"}]}`
	config, err := ParsePathProtectionConfig(jsonStr)
	if err != nil {
		t.Fatalf("Error parsing config: %v", err)
	}

	pp, err := NewPathProtector(config, "/jfs")
	if err != nil {
		t.Fatalf("Error creating path protector: %v", err)
	}
	v.PathProtector = pp

	// Try to lookup in the protected directory - should be blocked
	_, errno = v.Lookup(ctx, secretsDir.Inode, "secret.txt")
	if errno != syscall.EACCES {
		t.Errorf("Expected lookup to be blocked with EACCES, got errno=%d", errno)
	}

	// Lookup in unprotected path should succeed
	_, errno = v.Lookup(ctx, 1, "data")
	if errno != 0 {
		t.Errorf("Expected lookup to succeed for unprotected path, got errno=%d", errno)
	}
}

// TestReadProtectionWithReadonlyMode tests that Read method allows reads in readonly mode
func TestReadProtectionWithReadonlyMode(t *testing.T) {
	// Create VFS without path protection initially
	v, _ := createTestVFS(nil, "")
	ctx := NewLogContext(meta.NewContext(10, 1, []uint32{2, 3}))

	// First, create directory and file structure without protection
	dataDir, errno := v.Mkdir(ctx, 1, "data", 0755, 0)
	if errno != 0 {
		t.Fatalf("Failed to create /data directory: %v", errno)
	}

	gitDir, errno := v.Mkdir(ctx, dataDir.Inode, ".git", 0755, 0)
	if errno != 0 {
		t.Fatalf("Failed to create /data/.git directory: %v", errno)
	}

	// Create a file in the protected directory
	fe, fh, errno := v.Create(ctx, gitDir.Inode, "config", 0644, 0, syscall.O_RDWR)
	if errno != 0 {
		t.Fatalf("Failed to create file: %v", errno)
	}

	// Write some data to the file
	data := []byte("[core]\nrepositoryformatversion = 0")
	errno = v.Write(ctx, fe.Inode, data, 0, fh)
	if errno != 0 {
		t.Fatalf("Failed to write to file: %v", errno)
	}

	// Now enable path protection with readonly mode
	jsonStr := `{"rules":[{"pattern":"^/jfs/data/.*\\.git.*", "mode": "readonly"}]}`
	config, err := ParsePathProtectionConfig(jsonStr)
	if err != nil {
		t.Fatalf("Error parsing config: %v", err)
	}

	pp, err := NewPathProtector(config, "/jfs")
	if err != nil {
		t.Fatalf("Error creating path protector: %v", err)
	}
	v.PathProtector = pp

	// Read from readonly path - should succeed
	buf := make([]byte, 100)
	_, errno = v.Read(ctx, fe.Inode, buf, 0, fh)
	if errno != 0 {
		t.Errorf("Expected read to succeed for readonly path, got errno=%d", errno)
	}
}

// TestParseInvalidConfig tests that invalid regex pattern returns error in NewPathProtector
func TestParseInvalidConfig(t *testing.T) {
	invalidJSON := `{"rules":[{"pattern":"[invalid(regex", "mode": "readonly"}]}`
	config, err := ParsePathProtectionConfig(invalidJSON)
	if err != nil {
		t.Fatalf("ParsePathProtectionConfig should not validate regex, got error: %v", err)
	}

	// NewPathProtector should fail when compiling invalid regex
	_, err = NewPathProtector(config, "/mnt")
	if err == nil {
		t.Error("Expected error for invalid regex pattern in NewPathProtector")
	}
}
