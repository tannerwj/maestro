#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${1:-${VERSION:-}}"

if [[ -z "${VERSION}" ]]; then
  echo "usage: scripts/build_release.sh <version>" >&2
  exit 1
fi

OUT_DIR="${ROOT}/dist/${VERSION}"
mkdir -p "${OUT_DIR}"

platforms=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
)

for target in "${platforms[@]}"; do
  read -r goos goarch <<<"${target}"
  name="maestro_${VERSION}_${goos}_${goarch}"
  workdir="${OUT_DIR}/${name}"
  archive="${OUT_DIR}/${name}.tar.gz"

  rm -rf "${workdir}" "${archive}"
  mkdir -p "${workdir}"

  (
    cd "${ROOT}"
    GOOS="${goos}" GOARCH="${goarch}" \
      go build -trimpath -ldflags "-X main.version=${VERSION}" \
      -o "${workdir}/maestro" ./cmd/maestro
  )

  cp "${ROOT}/README.md" "${workdir}/README.md"
  cp "${ROOT}/LICENSE" "${workdir}/LICENSE" 2>/dev/null || true
  tar -C "${OUT_DIR}" -czf "${archive}" "${name}"
  rm -rf "${workdir}"
done

(
  cd "${OUT_DIR}"
  shasum -a 256 ./*.tar.gz > checksums.txt
)

echo "Release artifacts written to ${OUT_DIR}"
