/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

//nolint:errcheck
package meta

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"testing"

	"xorm.io/xorm"
)

func TestSQLiteClient(t *testing.T) {
	m, err := newSQLMeta("sqlite3", path.Join(t.TempDir(), "jfs-unit-test.db"), testConfig())
	if err != nil || m.Name() != "sqlite3" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestMySQLClient(t *testing.T) { //skip mutate
	m, err := newSQLMeta("mysql", "root:@/dev", testConfig())
	if err != nil || m.Name() != "mysql" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestPostgreSQLClient(t *testing.T) { //skip mutate
	if os.Getenv("SKIP_NON_CORE") == "true" {
		t.Skipf("skip non-core test")
	}
	m, err := newSQLMeta("postgres", "localhost:5432/test?sslmode=disable", testConfig())
	if err != nil || m.Name() != "postgres" {
		t.Fatalf("create meta: %s", err)
	}
	testMeta(t, m)
}

func TestPostgreSQLClientWithSearchPath(t *testing.T) { //skip mutate
	_, err := newSQLMeta("postgres", "localhost:5432/test?sslmode=disable&search_path=juicefs,public", testConfig())
	if !strings.Contains(err.Error(), "currently, only one schema is supported in search_path") {
		t.Fatalf("TestPostgreSQLClientWithSearchPath error: %s", err)
	}
}

func TestRecoveryMysqlPwd(t *testing.T) { //skip mutate
	testCase := []struct {
		addr   string
		expect string
	}{
		// no password
		{"root@(localhost:3306)/db1",
			"root@(localhost:3306)/db1",
		},
		// no password
		{"root:@(localhost:3306)/db1",
			"root:@(localhost:3306)/db1",
		},

		{"root::@@(localhost:3306)/db1",
			"root::@@(localhost:3306)/db1",
		},

		{"root:@:@(localhost:3306)/db1",
			"root:@:@(localhost:3306)/db1",
		},

		// no special char
		{"root:password@(localhost:3306)/db1",
			"root:password@(localhost:3306)/db1",
		},

		// set from env @
		{"root:pass%40word@(localhost:3306)/db1",
			"root:pass@word@(localhost:3306)/db1",
		},

		// direct pass special char @
		{"root:pass@word@(localhost:3306)/db1",
			"root:pass@word@(localhost:3306)/db1",
		},

		// set from env |
		{"root:pass%7Cword@(localhost:3306)/db1",
			"root:pass|word@(localhost:3306)/db1",
		},

		// direct pass special char |
		{"root:pass|word@(localhost:3306)/db1",
			"root:pass|word@(localhost:3306)/db1",
		},

		// set from env :
		{"root:pass%3Aword@(localhost:3306)/db1",
			"root:pass:word@(localhost:3306)/db1",
		},

		// direct pass special char :
		{"root:pass:word@(localhost:3306)/db1",
			"root:pass:word@(localhost:3306)/db1",
		},
	}
	for _, tc := range testCase {
		if got := recoveryMysqlPwd(tc.addr); got != tc.expect {
			t.Fatalf("recoveryMysqlPwd error: expect %s but got %s", tc.expect, got)
		}
	}
}

func TestGetCustomConfig(t *testing.T) {
	u := "mysql://root:password@tcp(localhost:3306)/db1?max_open_conns=100&notDefine=str"
	_, after, _ := strings.Cut(u, "?")
	query, err := url.ParseQuery(after)
	if err != nil {
		t.Fatalf("url parse query error: %s", err)
	}
	maxOpenConns, err := extractCustomConfig(&query, "max_open_conns", 1)
	if err != nil {
		t.Fatalf("getCustomConfig error: %s", err)
	}
	if maxOpenConns != 100 {
		t.Fatalf("getCustomConfig error: expect 100 but got %d", maxOpenConns)
	}
	if query.Has("max_open_conns") {
		t.Fatalf("getCustomConfig error: expect not found but found")
	}

	not, err := extractCustomConfig(&query, "notSetKey", "default")
	if err != nil {
		t.Fatalf("getCustomConfig error: %s", err)
	}
	if not != "default" {
		t.Fatalf("getCustomConfig error: expect default but got %s", not)
	}
	if !query.Has("notDefine") {
		t.Fatalf("getCustomConfig error: expect found but not")
	}

}

func TestGetRecursiveTreeNodes(t *testing.T) {
	if os.Getenv("SKIP_NON_CORE") == "true" {
		t.Skipf("skip non-core test")
	}
	m, err := newSQLMeta("postgres", "localhost:5432/test?sslmode=disable", testConfig())
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}

	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}

	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("initialize failed: %s", err)
	}

	// Create the same directory structure as in the clone test
	// cloneDir/
	// ├── dir/
	// └── dir1/
	//    ├── dir2/
	//    │ ├── dir3/
	//    │ │ └── file3
	//    │ ├── file2
	//    │ └── file2Hardlink
	//    ├── file1
	//    └── file1Symlink -> file1

	var cloneDir Ino
	if eno := m.Mkdir(Background(), RootInode, "cloneDir", 0777, 022, 0, &cloneDir, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir1 Ino
	if eno := m.Mkdir(Background(), cloneDir, "dir1", 0777, 022, 0, &dir1, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir Ino
	if eno := m.Mkdir(Background(), cloneDir, "dir", 0777, 022, 0, &dir, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir2 Ino
	if eno := m.Mkdir(Background(), dir1, "dir2", 0777, 022, 0, &dir2, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir3 Ino
	if eno := m.Mkdir(Background(), dir2, "dir3", 0777, 022, 0, &dir3, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var file1 Ino
	if eno := m.Mknod(Background(), dir1, "file1", TypeFile, 0777, 022, 0, "", &file1, nil); eno != 0 {
		t.Fatalf("mknod: %s", eno)
	}
	var file2 Ino
	if eno := m.Mknod(Background(), dir2, "file2", TypeFile, 0777, 022, 0, "", &file2, nil); eno != 0 {
		t.Fatalf("mknod: %s", eno)
	}
	var file3 Ino
	if eno := m.Mknod(Background(), dir3, "file3", TypeFile, 0777, 022, 0, "", &file3, nil); eno != 0 {
		t.Fatalf("mknod: %s", eno)
	}
	var file1Symlink Ino
	if eno := m.Symlink(Background(), dir1, "file1Symlink", "file1", &file1Symlink, nil); eno != 0 {
		t.Fatalf("symlink: %s", eno)
	}
	if eno := m.Link(Background(), file2, dir2, "file2Hardlink", nil); eno != 0 {
		t.Fatalf("hardlink: %s", eno)
	}

	// Test the getRecursiveTreeNodes function
	sqlMeta := m.(*dbMeta)
	err = sqlMeta.roTxn(Background(), func(s *xorm.Session) error {
		cursor, err := sqlMeta.getRecursiveTreeNodes(s, cloneDir)
		if err != nil {
			t.Fatalf("getRecursiveTreeNodes failed: %s", err)
		}
		defer cursor.Close()

		// Test batch reading with different batch sizes
		var nodes []treeNodeSimple

		// First test: read in batches of 3
		batchSize := 3
		batchCount := 0
		for {
			batch, hasMore, err := cursor.NextBatch(batchSize)
			if err != nil {
				t.Fatalf("cursor.NextBatch(%d) failed: %s", batchSize, err)
			}
			if len(batch) == 0 {
				break
			}
			t.Logf("Batch %d: got %d nodes", batchCount, len(batch))
			nodes = append(nodes, batch...)
			batchCount++
			if !hasMore {
				break
			}
		}

		t.Logf("Total batches read: %d", batchCount)

		// Test edge cases
		// Test with batch size larger than available data
		cursor2, err := sqlMeta.getRecursiveTreeNodes(s, cloneDir)
		if err != nil {
			t.Fatalf("getRecursiveTreeNodes failed: %s", err)
		}
		defer cursor2.Close()

		largeBatch, hasMore, err := cursor2.NextBatch(50) // Request more than the 10 available
		if err != nil {
			t.Fatalf("large batch failed: %s", err)
		}
		t.Logf("Large batch test: requested 50, got %d nodes, hasMore=%v", len(largeBatch), hasMore)

		if len(largeBatch) != 10 || hasMore {
			t.Fatalf("large batch should return all 10 nodes with hasMore=false")
		}

		// Debug: print all nodes with edge information
		t.Logf("Found %d nodes:", len(nodes))
		for i, node := range nodes {
			edgeName := string(node.Edge.Name)
			if edgeName == "" {
				edgeName = "<root>"
			}
			t.Logf("Node[%d]: inode=%d, type=%d, level=%d, edge_name='%s', edge_parent=%d",
				i, node.Inode, node.Type, node.Level, edgeName, node.Edge.Parent)
		}

		// Expected 10 nodes: each represents a unique path (edge) to a node
		// The hardlink creates two different edges to the same inode, which is now clearly visible
		expectedNodeCount := 10
		if len(nodes) != expectedNodeCount {
			t.Fatalf("expected %d nodes, got %d", expectedNodeCount, len(nodes))
		}

		// Verify nodes are ordered by level
		for i := 1; i < len(nodes); i++ {
			if nodes[i].Level < nodes[i-1].Level {
				t.Fatalf("nodes not ordered by level: node %d has level %d, node %d has level %d",
					i-1, nodes[i-1].Level, i, nodes[i].Level)
			}
		}

		// Verify root node is at level 0 and has no edge name (since it's the starting point)
		if nodes[0].Level != 0 || nodes[0].Inode != cloneDir || len(nodes[0].Edge.Name) > 0 {
			t.Fatalf("expected root node %d at level 0 with no edge name, got inode %d at level %d with edge name '%s'",
				cloneDir, nodes[0].Inode, nodes[0].Level, string(nodes[0].Edge.Name))
		}

		// Count nodes by level
		levelCounts := make(map[int]int)
		for _, node := range nodes {
			levelCounts[node.Level]++
		}

		// Expected structure with edge information:
		// Level 0: cloneDir (1 path) - root has no edge
		// Level 1: dir, dir1 (2 paths) - edges from cloneDir
		// Level 2: dir2, file1, file1Symlink (3 paths) - edges from dir1
		// Level 3: dir3, file2 (as "file2"), file2 (as "file2Hardlink") (3 paths) - different edges to same inode
		// Level 4: file3 (1 path) - edge from dir3
		expectedLevels := map[int]int{0: 1, 1: 2, 2: 3, 3: 3, 4: 1}
		for level, expectedCount := range expectedLevels {
			if levelCounts[level] != expectedCount {
				t.Fatalf("level %d: expected %d nodes, got %d", level, expectedCount, levelCounts[level])
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("transaction failed: %s", err)
	}
}

func TestCloneTree(t *testing.T) {
	m, err := newSQLMeta("postgres", "localhost:5432/test?sslmode=disable", testConfig())
	if err != nil {
		t.Fatalf("create meta: %s", err)
	}

	if err := m.Reset(); err != nil {
		t.Fatalf("reset meta: %s", err)
	}

	m.OnMsg(DeleteSlice, func(args ...interface{}) error { return nil })
	if err := m.Init(testFormat(), true); err != nil {
		t.Fatalf("initialize failed: %s", err)
	}

	// Create the same directory structure as before
	var cloneDir Ino
	if eno := m.Mkdir(Background(), RootInode, "cloneDir", 0777, 022, 0, &cloneDir, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir1 Ino
	if eno := m.Mkdir(Background(), cloneDir, "dir1", 0777, 022, 0, &dir1, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir Ino
	if eno := m.Mkdir(Background(), cloneDir, "dir", 0777, 022, 0, &dir, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir2 Ino
	if eno := m.Mkdir(Background(), dir1, "dir2", 0777, 022, 0, &dir2, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var dir3 Ino
	if eno := m.Mkdir(Background(), dir2, "dir3", 0777, 022, 0, &dir3, nil); eno != 0 {
		t.Fatalf("mkdir: %s", eno)
	}
	var file1 Ino
	if eno := m.Mknod(Background(), dir1, "file1", TypeFile, 0777, 022, 0, "", &file1, nil); eno != 0 {
		t.Fatalf("mknod: %s", eno)
	}
	var file2 Ino
	if eno := m.Mknod(Background(), dir2, "file2", TypeFile, 0777, 022, 0, "", &file2, nil); eno != 0 {
		t.Fatalf("mknod: %s", eno)
	}
	var file3 Ino
	if eno := m.Mknod(Background(), dir3, "file3", TypeFile, 0777, 022, 0, "", &file3, nil); eno != 0 {
		t.Fatalf("mknod: %s", eno)
	}
	var file1Symlink Ino
	if eno := m.Symlink(Background(), dir1, "file1Symlink", "file1", &file1Symlink, nil); eno != 0 {
		t.Fatalf("symlink: %s", eno)
	}
	if eno := m.Link(Background(), file2, dir2, "file2Hardlink", nil); eno != 0 {
		t.Fatalf("hardlink: %s", eno)
	}

	// Artificially create some chunks and chunk references to test cloning
	// This simulates files having actual data without needing object storage
	sqlMeta := m.(*dbMeta)
	err = sqlMeta.txn(func(s *xorm.Session) error {
		// Update file lengths to simulate they have data
		if _, err := s.Exec("UPDATE "+sqlMeta.tablePrefix+"node SET length = ? WHERE inode = ?", 100, file1); err != nil {
			return err
		}
		if _, err := s.Exec("UPDATE "+sqlMeta.tablePrefix+"node SET length = ? WHERE inode = ?", 200, file2); err != nil {
			return err
		}
		if _, err := s.Exec("UPDATE "+sqlMeta.tablePrefix+"node SET length = ? WHERE inode = ?", 50, file3); err != nil {
			return err
		}

		// Create some test chunks with proper slice data
		// Each slice references a chunk in object storage via chunkId
		slice1 := marshalSlice(0, 1001, 100, 0, 100) // pos=0, chunkId=1001, size=100, off=0, len=100
		slice2 := marshalSlice(0, 1002, 200, 0, 200) // pos=0, chunkId=1002, size=200, off=0, len=200
		slice3 := marshalSlice(0, 1003, 50, 0, 50)   // pos=0, chunkId=1003, size=50, off=0, len=50

		testChunks := []chunk{
			{Inode: file1, Indx: 0, Slices: slice1},
			{Inode: file2, Indx: 0, Slices: slice2},
			{Inode: file3, Indx: 0, Slices: slice3},
		}

		if _, err := s.Insert(&testChunks); err != nil {
			return err
		}

		// Create some test chunk references (simulate chunks in object storage)
		testChunkRefs := []sliceRef{
			{Id: 1001, Size: 100, Refs: 1},
			{Id: 1002, Size: 200, Refs: 1},
			{Id: 1003, Size: 50, Refs: 1},
		}

		if _, err := s.Insert(&testChunkRefs); err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		t.Fatalf("failed to create test chunks: %s", err)
	}

	t.Logf("Created test chunks and references for files with lengths: file1=100, file2=200, file3=50")

	// Now clone the directory tree
	var clonedIno Ino
	var totalCloned uint64

	t.Logf("Cloning directory tree from inode %d", cloneDir)
	eno := sqlMeta.cloneTree(Background(), cloneDir, RootInode, "clonedDir", &clonedIno, MODE_MASK_R|MODE_MASK_W|MODE_MASK_X, 022, &totalCloned)
	if eno != 0 {
		t.Fatalf("cloneTree failed: %s", eno)
	}

	t.Logf("Clone completed: cloned %d nodes, new root inode: %d", totalCloned, clonedIno)

	// Compare the original and cloned trees using our cursor
	originalNodes := getTreeNodes(t, sqlMeta, cloneDir, "original")
	clonedNodes := getTreeNodes(t, sqlMeta, clonedIno, "cloned")

	// Verify same number of nodes
	if len(originalNodes) != len(clonedNodes) {
		t.Fatalf("tree structure mismatch: original has %d nodes, clone has %d nodes",
			len(originalNodes), len(clonedNodes))
	}

	// Compare trees by logical structure instead of array position
	originalByPath := make(map[string]treeNodeSimple)
	clonedByPath := make(map[string]treeNodeSimple)

	// Build path maps
	for _, node := range originalNodes {
		path := buildNodePath(node, originalNodes)
		originalByPath[path] = node
	}

	for _, node := range clonedNodes {
		path := buildNodePath(node, clonedNodes)
		clonedByPath[path] = node
	}

	// Debug: show all paths
	t.Logf("Original tree paths:")
	for path := range originalByPath {
		t.Logf("  %s", path)
	}
	t.Logf("Cloned tree paths:")
	for path := range clonedByPath {
		t.Logf("  %s", path)
	}

	// Verify all paths exist in both trees
	if len(originalByPath) != len(clonedByPath) {
		t.Fatalf("Path count mismatch: original has %d unique paths, clone has %d",
			len(originalByPath), len(clonedByPath))
	}

	// Compare by logical path instead of array position
	for path, origNode := range originalByPath {
		clonedNode, exists := clonedByPath[path]
		if !exists {
			t.Errorf("Path '%s' missing in cloned tree", path)
			continue
		}

		if origNode.Level != clonedNode.Level {
			t.Errorf("Path '%s' level mismatch: original=%d, clone=%d",
				path, origNode.Level, clonedNode.Level)
		}

		if origNode.Type != clonedNode.Type {
			t.Errorf("Path '%s' type mismatch: original=%d, clone=%d",
				path, origNode.Type, clonedNode.Type)
		}
	}

	t.Logf("Clone verification successful: both trees have identical structure")

	// Verify dirStats are properly cloned
	if err := verifyDirStatsCloning(t, sqlMeta, originalNodes, clonedNodes); err != nil {
		t.Fatalf("dirStats verification failed: %s", err)
	}

	// Verify chunk references are properly handled
	if err := verifyChunkRefsCloning(t, sqlMeta, originalNodes, clonedNodes); err != nil {
		t.Fatalf("chunk_refs verification failed: %s", err)
	}
}

// Helper function to get all nodes from a tree
func getTreeNodes(t *testing.T, sqlMeta *dbMeta, rootIno Ino, label string) []treeNodeSimple {
	var nodes []treeNodeSimple

	err := sqlMeta.roTxn(Background(), func(s *xorm.Session) error {
		cursor, err := sqlMeta.getRecursiveTreeNodes(s, rootIno)
		if err != nil {
			return err
		}
		defer cursor.Close()

		for {
			batch, hasMore, err := cursor.NextBatch(100)
			if err != nil {
				return err
			}
			if len(batch) == 0 {
				break
			}
			nodes = append(nodes, batch...)
			if !hasMore {
				break
			}
		}
		return nil
	})

	if err != nil {
		t.Fatalf("failed to get %s tree nodes: %s", label, err)
	}

	t.Logf("%s tree has %d nodes:", label, len(nodes))
	for i, node := range nodes {
		edgeName := string(node.Edge.Name)
		if edgeName == "" {
			edgeName = "<root>"
		}
		t.Logf("  [%d] inode=%d, type=%d, level=%d, edge_name='%s'",
			i, node.Inode, node.Type, node.Level, edgeName)
	}

	return nodes
}

// buildNodePath constructs the full path for a node by traversing up the tree
func buildNodePath(targetNode treeNodeSimple, allNodes []treeNodeSimple) string {
	// Special case for root node
	if targetNode.Level == 0 {
		return "/"
	}

	// Build a parent->children map for efficient lookups
	parentToNodes := make(map[Ino][]treeNodeSimple)
	for _, node := range allNodes {
		if node.Level > 0 { // Skip root node
			parentToNodes[node.Edge.Parent] = append(parentToNodes[node.Edge.Parent], node)
		}
	}

	// Build path components from target up to root
	var pathComponents []string
	current := targetNode

	for current.Level > 0 {
		edgeName := string(current.Edge.Name)
		if edgeName == "" {
			edgeName = "<unnamed>"
		}
		pathComponents = append([]string{edgeName}, pathComponents...) // Prepend

		// Find parent node
		parentIno := current.Edge.Parent
		parentFound := false
		for _, node := range allNodes {
			if node.Inode == parentIno {
				current = node
				parentFound = true
				break
			}
		}

		if !parentFound {
			// This shouldn't happen in a well-formed tree, but handle gracefully
			break
		}
	}

	// Join with slashes to create full path
	if len(pathComponents) == 0 {
		return "/"
	}
	return "/" + strings.Join(pathComponents, "/")
}

// verifyDirStatsCloning checks that directory statistics are properly cloned
func verifyDirStatsCloning(t *testing.T, sqlMeta *dbMeta, originalNodes, clonedNodes []treeNodeSimple) error {
	// Build maps of directory inodes
	originalDirs := make(map[Ino]treeNodeSimple)
	clonedDirs := make(map[Ino]treeNodeSimple)

	for _, node := range originalNodes {
		if node.Type == TypeDirectory {
			originalDirs[node.Inode] = node
		}
	}
	for _, node := range clonedNodes {
		if node.Type == TypeDirectory {
			clonedDirs[node.Inode] = node
		}
	}

	// Check dirStats for each original directory
	return sqlMeta.roTxn(Background(), func(s *xorm.Session) error {
		for origIno, origNode := range originalDirs {
			// Find corresponding cloned directory by path
			origPath := buildNodePath(origNode, originalNodes)
			var clonedIno Ino
			found := false
			for clonedNodeIno, clonedNode := range clonedDirs {
				clonedPath := buildNodePath(clonedNode, clonedNodes)
				if origPath == clonedPath {
					clonedIno = clonedNodeIno
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("could not find cloned directory for path %s", origPath)
			}

			// Check if original has dirStats
			var origStats dirStats
			origHasStats, err := s.Where("inode = ?", origIno).Get(&origStats)
			if err != nil {
				return fmt.Errorf("failed to query original dirStats for inode %d: %v", origIno, err)
			}

			// Check if cloned has corresponding dirStats
			var clonedStats dirStats
			clonedHasStats, err := s.Where("inode = ?", clonedIno).Get(&clonedStats)
			if err != nil {
				return fmt.Errorf("failed to query cloned dirStats for inode %d: %v", clonedIno, err)
			}

			// Both should have same dirStats presence
			if origHasStats != clonedHasStats {
				return fmt.Errorf("dirStats mismatch for path %s: original has %v, cloned has %v",
					origPath, origHasStats, clonedHasStats)
			}

			// If both have stats, verify they match
			if origHasStats && clonedHasStats {
				if origStats.DataLength != clonedStats.DataLength ||
					origStats.UsedSpace != clonedStats.UsedSpace ||
					origStats.UsedInodes != clonedStats.UsedInodes {
					return fmt.Errorf("dirStats values mismatch for path %s", origPath)
				}
				t.Logf("dirStats verified for %s: DataLength=%d, UsedSpace=%d, UsedInodes=%d",
					origPath, origStats.DataLength, origStats.UsedSpace, origStats.UsedInodes)
			}
		}
		return nil
	})
}

// verifyChunkRefsCloning checks that file chunks and references are properly handled
func verifyChunkRefsCloning(t *testing.T, sqlMeta *dbMeta, originalNodes, clonedNodes []treeNodeSimple) error {
	// Build maps of unique file inodes (avoid duplicates from hardlinks)
	originalFileInodes := make(map[Ino]bool)
	clonedFileInodes := make(map[Ino]bool)

	// Build inode mapping from original to cloned by comparing paths
	inoMapping := make(map[Ino]Ino)

	for _, origNode := range originalNodes {
		if origNode.Type == TypeFile {
			originalFileInodes[origNode.Inode] = true

			// Find corresponding cloned node by path
			origPath := buildNodePath(origNode, originalNodes)
			for _, clonedNode := range clonedNodes {
				if clonedNode.Type == TypeFile {
					clonedPath := buildNodePath(clonedNode, clonedNodes)
					if origPath == clonedPath {
						inoMapping[origNode.Inode] = clonedNode.Inode
						clonedFileInodes[clonedNode.Inode] = true
						break
					}
				}
			}
		}
	}

	return sqlMeta.roTxn(Background(), func(s *xorm.Session) error {
		for origIno := range originalFileInodes {
			clonedIno, exists := inoMapping[origIno]
			if !exists {
				return fmt.Errorf("could not find cloned inode for original inode %d", origIno)
			}

			// Get chunks for original file inode
			var origChunks []chunk
			if err := s.Where("inode = ?", origIno).Find(&origChunks); err != nil {
				return fmt.Errorf("failed to query original chunks for inode %d: %v", origIno, err)
			}

			// Get chunks for cloned file
			var clonedChunks []chunk
			if err := s.Where("inode = ?", clonedIno).Find(&clonedChunks); err != nil {
				return fmt.Errorf("failed to query cloned chunks for inode %d: %v", clonedIno, err)
			}

			// Should have same number of chunks
			if len(origChunks) != len(clonedChunks) {
				return fmt.Errorf("chunk count mismatch for inode %d->%d: original has %d, cloned has %d",
					origIno, clonedIno, len(origChunks), len(clonedChunks))
			}

			// Create maps by chunk index for proper matching
			origChunkMap := make(map[uint32]chunk)
			clonedChunkMap := make(map[uint32]chunk)

			for _, c := range origChunks {
				origChunkMap[c.Indx] = c
			}
			for _, c := range clonedChunks {
				clonedChunkMap[c.Indx] = c
			}

			// Verify each chunk's slices by matching chunk index
			for chunkIdx, origChunk := range origChunkMap {
				clonedChunk, exists := clonedChunkMap[chunkIdx]
				if !exists {
					return fmt.Errorf("chunk index %d missing in cloned inode %d", chunkIdx, clonedIno)
				}

				if !bytes.Equal(origChunk.Slices, clonedChunk.Slices) {
					return fmt.Errorf("chunk slices mismatch for inode %d->%d chunk[%d]", origIno, clonedIno, chunkIdx)
				}

				t.Logf("Chunk verified for inode %d->%d[%d]: %d bytes of slice data",
					origIno, clonedIno, chunkIdx, len(origChunk.Slices))
			}

			// Check for any extra chunks in cloned that shouldn't be there
			for chunkIdx := range clonedChunkMap {
				if _, exists := origChunkMap[chunkIdx]; !exists {
					return fmt.Errorf("unexpected chunk index %d in cloned inode %d", chunkIdx, clonedIno)
				}
			}
		}
		return nil
	})
}
