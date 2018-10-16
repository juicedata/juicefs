package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolvedReferences(t *testing.T) {
	json := `{
		"operations": {
			"OperationName": {
				"input": { "shape": "TestName" }
			}
		},
		"shapes": {
			"TestName": {
				"type": "structure",
				"members": {
					"memberName1": { "shape": "OtherTest" },
					"memberName2": { "shape": "OtherTest" }
				}
			},
			"OtherTest": { "type": "string" }
		}
	}`
	a := API{}
	a.AttachString(json)
	assert.Equal(t, len(a.Shapes["OtherTest"].refs), 2)
}

func TestRenamedShapes(t *testing.T) {
	json := `{
		"operations": {
			"OperationName": {
				"input": { "shape": "TestRequest" },
				"output": { "shape": "TestResult" }
			}
		},
		"shapes": {
			"TestRequest": {
				"type": "structure",
				"members": {
					"memberName1": { "shape": "TestVpnIcmp" },
					"memberName2": { "shape": "TestVpnIcmp" }
				}
			},
			"TestVpnIcmp": { "type": "structure", "members": {} },
			"TestResult": {
				"type": "structure",
				"members": {
					"memberName1": { "shape": "TestVpnIcmp" }
				}
			}
		}
	}`
	a := API{}
	a.AttachString(json)
	assert.Nil(t, a.Shapes["TestRequest"])
	assert.NotNil(t, a.Shapes["OperationNameInput"])
	assert.Nil(t, a.Shapes["OperationNameInput"].MemberRefs["memberName1"])
	assert.NotNil(t, a.Shapes["OperationNameInput"].MemberRefs["MemberName1"])
	assert.Nil(t, a.Shapes["OperationNameInput"].MemberRefs["memberName2"])
	assert.NotNil(t, a.Shapes["OperationNameInput"].MemberRefs["MemberName2"])

	assert.Nil(t, a.Shapes["TestResult"])
	assert.NotNil(t, a.Shapes["OperationNameOutput"])

	assert.Nil(t, a.Shapes["TestVpnIcmp"])
	assert.NotNil(t, a.Shapes["TestVPNICMP"])
}
