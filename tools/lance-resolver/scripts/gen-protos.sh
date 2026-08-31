#!/usr/bin/env bash
set -euo pipefail

# Regenerate the Go protobuf files used by tools/lance-resolver from an
# explicitly pinned Lance version. The upstream .proto files deliberately do
# not contain go_package options, so the import paths are supplied here via
# protoc --go_opt=M<proto>=<import_path> mappings. Only the generated .pb.go
# files are committed; do not commit downloaded .proto files.

LANCE_VERSION="${LANCE_VERSION:-v11.0.0-rc.1}"
PROTOC_GEN_GO_VERSION="v1.36.11"
# protoc must be pinned alongside protoc-gen-go for reproducible generation;
# the matching release binary is downloaded instead of trusting PATH.
PROTOC_VERSION="${PROTOC_VERSION:-36.0}"

TOOL_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO_ROOT="$(cd "${TOOL_DIR}/../.." && pwd)"
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

echo "Installing protoc ${PROTOC_VERSION}"
case "$(uname -s)-$(uname -m)" in
  Linux-x86_64)   PROTOC_OS=linux-x86_64 ;;
  Linux-aarch64)  PROTOC_OS=linux-aarch_64 ;;
  Darwin-x86_64)  PROTOC_OS=osx-x86_64 ;;
  Darwin-arm64)   PROTOC_OS=osx-aarch_64 ;;
  MINGW*|MSYS*|CYGWIN*) PROTOC_OS=win64 ;;
  *) echo "unsupported platform $(uname -s)-$(uname -m)" >&2; exit 1 ;;
esac
curl -fsSL -o "${TMP_DIR}/protoc.zip" \
  "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-${PROTOC_OS}.zip"
if command -v unzip >/dev/null 2>&1; then
  unzip -q "${TMP_DIR}/protoc.zip" -d "${TMP_DIR}"
else
  python3 -m zipfile -e "${TMP_DIR}/protoc.zip" "${TMP_DIR}"
fi
"${TMP_DIR}/bin/protoc" --version

echo "Generating Go code"
(
  cd "${TMP_DIR}"
  PATH="${TMP_DIR}/bin:${PATH}" protoc \
    --go_out="${OUT_DIR}" \
    --go_opt=paths=source_relative \
    --go_opt=Mfile.proto=github.com/juicedata/juicefs/tools/lance-resolver/proto/lance \
    --go_opt=Mtable.proto=github.com/juicedata/juicefs/tools/lance-resolver/proto/lance \
    -I . -I "${TMP_DIR}/include" file.proto table.proto
  PATH="${TMP_DIR}/bin:${PATH}" protoc \
    --go_out="${FILE2_OUT_DIR}" \
    --go_opt=paths=source_relative \
    --go_opt=Mfile2.proto=github.com/juicedata/juicefs/tools/lance-resolver/proto/lance/file2 \
    -I . -I "${TMP_DIR}/include" file2.proto
)

echo "Generated ${OUT_DIR}/file.pb.go, ${OUT_DIR}/table.pb.go and ${FILE2_OUT_DIR}/file2.pb.go"
