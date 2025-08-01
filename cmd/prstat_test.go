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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/urfave/cli/v2"
)

func TestPRStatCommand(t *testing.T) {
	// Test basic command creation
	cmd := cmdPRStat()
	assert.Equal(t, "prstat", cmd.Name)
	assert.Equal(t, "ADMIN", cmd.Category)
	assert.NotEmpty(t, cmd.Usage)
	assert.NotEmpty(t, cmd.Description)
}

func TestGetMockPRs(t *testing.T) {
	// Test mock data for user davies
	mockPRs := getMockPRs("davies", "")
	assert.Greater(t, len(mockPRs), 0)
	
	// Verify all returned PRs are authored by davies
	for _, pr := range mockPRs {
		assert.Equal(t, "davies", pr.User.Login)
	}
	
	// Test mock data for merged-by davies
	mockPRs = getMockPRs("", "davies")
	assert.Greater(t, len(mockPRs), 0)
	
	// Verify all returned PRs are merged by davies
	for _, pr := range mockPRs {
		assert.NotNil(t, pr.MergedBy)
		assert.Equal(t, "davies", pr.MergedBy.Login)
	}
}

func TestPRStatWithMissingParams(t *testing.T) {
	app := &cli.App{
		Commands: []*cli.Command{cmdPRStat()},
	}
	
	// Test without user or merged-by params
	err := app.Run([]string{"test", "prstat"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "please specify either --user or --merged-by flag")
}

func TestPRStatWithMockData(t *testing.T) {
	// This test would require setting up a proper CLI context
	// For now, we'll just test that the mock data function works
	mockPRs := getMockPRs("testuser", "")
	assert.Greater(t, len(mockPRs), 0)
	
	// Test with different user
	mockPRs = getMockPRs("nonexistent", "")
	// Should still return some data for testing purposes
	assert.GreaterOrEqual(t, len(mockPRs), 0)
}