#!/usr/bin/env bash
set -euo pipefail

# Regenerate the Go protobuf files used by tools/lance-resolver from an
# explicitly pinned Lance version. The upstream .proto files deliberately do
# not contain go_package options, so the import paths are supplied here via
# protoc --go_opt=M<proto>=<import_path> mappings. Only the generated .pb.go
# files are committed; do not commit downloaded .proto files.

LANCE_VERSION="${LANCE_VERSION:-v11.0.0-rc.1}"
PROTOC_GEN_GO_VERSION="v1.36.11"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT_DIR="${REPO_ROOT}/tools/lance-resolver/proto/lance"
FILE2_OUT_DIR="${OUT_DIR}/file2"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

echo "Downloading Lance proto files at ${LANCE_VERSION}"
curl -fsSL -o "${TMP_DIR}/file.proto" \
  "https://raw.githubusercontent.com/lance-format/lance/${LANCE_VERSION}/protos/file.proto"
curl -fsSL -o "${TMP_DIR}/table.proto" \
  "https://raw.githubusercontent.com/lance-format/lance/${LANCE_VERSION}/protos/table.proto"
curl -fsSL -o "${TMP_DIR}/file2.proto" \
  "https://raw.githubusercontent.com/lance-format/lance/${LANCE_VERSION}/protos/file2.proto"

mkdir -p "${TMP_DIR}/bin" "${FILE2_OUT_DIR}"
echo "Installing protoc-gen-go ${PROTOC_GEN_GO_VERSION}"
GOBIN="${TMP_DIR}/bin" go install "google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION}"

echo "Generating Go code"
(
  cd "${TMP_DIR}"
  PATH="${TMP_DIR}/bin:${PATH}" protoc \
    --go_out="${OUT_DIR}" \
    --go_opt=paths=source_relative \
    --go_opt=Mfile.proto=github.com/juicedata/juicefs/tools/lance-resolver/proto/lance \
    --go_opt=Mtable.proto=github.com/juicedata/juicefs/tools/lance-resolver/proto/lance \
    -I . file.proto table.proto
  PATH="${TMP_DIR}/bin:${PATH}" protoc \
    --go_out="${FILE2_OUT_DIR}" \
    --go_opt=paths=source_relative \
    --go_opt=Mfile2.proto=github.com/juicedata/juicefs/tools/lance-resolver/proto/lance/file2 \
    -I . file2.proto
)

echo "Generated ${OUT_DIR}/file.pb.go, ${OUT_DIR}/table.pb.go and ${FILE2_OUT_DIR}/file2.pb.go"
