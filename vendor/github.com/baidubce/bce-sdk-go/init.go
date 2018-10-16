/*
 * Copyright 2017 Baidu, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
 * except in compliance with the License. You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software distributed under the
 * License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
 * either express or implied. See the License for the specific language governing permissions
 * and limitations under the License.
 */

// init.go - just import the sub packages

// Package sdk imports all sub packages to build all of them when calling `go install', `go build'
// or `go get' commands.
package sdk

import (
	_ "github.com/baidubce/bce-sdk-go/auth"
	_ "github.com/baidubce/bce-sdk-go/bce"
	_ "github.com/baidubce/bce-sdk-go/http"
	_ "github.com/baidubce/bce-sdk-go/services/bos"
	_ "github.com/baidubce/bce-sdk-go/services/sts"
	_ "github.com/baidubce/bce-sdk-go/util"
	_ "github.com/baidubce/bce-sdk-go/util/log"
)
