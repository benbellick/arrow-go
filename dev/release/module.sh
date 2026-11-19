#!/usr/bin/env bash
#
# Licensed to the Apache Software Foundation (ASF) under one
# or more contributor license agreements.  See the NOTICE file
# distributed with this work for additional information
# regarding copyright ownership.  The ASF licenses this file
# to you under the Apache License, Version 2.0 (the
# "License"); you may not use this file except in compliance
# with the License.  You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

# Sets the release settings for a separately released Go module.
#
# Usage: source module.sh <module>, where <module> is arrow-go or exprs.
#
# Each module has its own version, tags, source archive and vote.

case "$1" in
arrow-go)
  RELEASE_ARCHIVE_TREE="HEAD"
  RELEASE_VERSION_FILE="arrow/doc.go"
  RELEASE_TAG_PREFIX="v"
  RELEASE_ARCHIVE_PREFIX="apache-arrow-go-"
  RELEASE_NAME="Apache Arrow Go"
  ;;
exprs)
  RELEASE_ARCHIVE_TREE="HEAD:arrow/compute/exprs"
  RELEASE_VERSION_FILE="arrow/compute/exprs/doc.go"
  RELEASE_TAG_PREFIX="arrow/compute/exprs/v"
  RELEASE_ARCHIVE_PREFIX="apache-arrow-go-exprs-"
  RELEASE_NAME="Apache Arrow Go exprs"
  ;;
*)
  echo "Unknown module: $1 (expected arrow-go or exprs)" >&2
  exit 1
  ;;
esac

release_version() {
  grep -o '^const PkgVersion = ".*"' "$1/${RELEASE_VERSION_FILE}" |
    sed \
      -e 's/^const PkgVersion = "//' \
      -e 's/"$//'
}
