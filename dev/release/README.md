<!---
  Licensed to the Apache Software Foundation (ASF) under one
  or more contributor license agreements.  See the NOTICE file
  distributed with this work for additional information
  regarding copyright ownership.  The ASF licenses this file
  to you under the Apache License, Version 2.0 (the
  "License"); you may not use this file except in compliance
  with the License.  You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

  Unless required by applicable law or agreed to in writing,
  software distributed under the License is distributed on an
  "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
  KIND, either express or implied.  See the License for the
  specific language governing permissions and limitations
  under the License.
-->

# Release

## Overview

  1. Test the revision to be released
  2. Increment version number in `arrow/doc.go`
  3. Prepare RC and vote (detailed later)
  4. Publish (detailed later)

### Prepare RC and vote

Run `dev/release/release_rc.sh` on a working copy of `git@github.com:apache/arrow-go` not your fork:

```console
$ git clone git@github.com:apache/arrow-go.git
$ dev/release/release_rc.sh ${RC}
(Send a vote email to dev@arrow.apache.org.
 You can use a draft shown by release_rc.sh for the email.)
```

Here is an example to release RC1:

```console
$ GH_TOKEN=${YOUR_GITHUB_TOKEN} dev/release/release_rc.sh 1
```

The argument of `release_rc.sh` is the RC number. If RC1 has a problem, we'll increment the RC number such as RC2, RC3 and so on.

Requirements to run `release_rc.sh`:

  * You must be an Apache Arrow committer or PMC member
  * You must prepare your PGP key for signing

If you don't have a PGP key, https://infra.apache.org/release-signing.html#generate may be helpful.

Your PGP key must be registered to the followings:

  * https://dist.apache.org/repos/dist/dev/arrow/KEYS
  * https://dist.apache.org/repos/dist/release/arrow/KEYS

See the header comment of them how to add a PGP key.

Apache arrow committers can update them by Subversion client with their ASF account. e.g.:

```console
$ svn co https://dist.apache.org/repos/dist/dev/arrow
$ cd arrow
$ editor KEYS
$ svn ci KEYS
```

### Publish

We need to do the followings to publish a new release:

  * Publish to apache.org

Run `dev/release/release.sh` to publish to apache.org:

```console
$ GH_TOKEN=${YOUR_GITHUB_TOKEN} dev/release/release.sh ${VERSION} ${RC}
```

Here is an example to release 18.0.0 RC1:

```console
$ GH_TOKEN=${YOUR_GITHUB_TOKEN} dev/release/release.sh 18.0.0 1
```

Add the release to ASF's report database via [Apache Committee Report Helper](https://reporter.apache.org/addrelease.html?arrow).

### Verify

We have a script to verify a RC.

You must install the following commands to use the script:

  * `curl`
  * `gpg`
  * `shasum` or `sha256sum`/`sha512sum`
  * `tar`

You don't need to install Go. If Go doesn't exist in system, the latest Go is automatically installed only for verification.

To verify a RC, run the following command line:

```console
$ dev/release/verify_rc.sh ${VERSION} ${RC}
```

Here is an example to verify the release 18.0.0 RC1:

```console
$ dev/release/verify_rc.sh 18.0.0 1
```

If the verification is successful, the message `RC looks good!` is shown.

## Releasing `arrow/compute/exprs`

`arrow/compute/exprs` is a separate Go module,
`github.com/apache/arrow-go/arrow/compute/exprs`. It is versioned and
released separately from the rest of Arrow Go, so that it can follow
`substrait-go` major versions without requiring an Arrow Go major release.

Its release uses the same scripts and steps as above, with `exprs` as
an extra argument:

  1. Increment the version number in `arrow/compute/exprs/doc.go`
  2. Prepare RC and vote: `dev/release/release_rc.sh ${RC} exprs`
  3. Publish: `dev/release/release.sh ${VERSION} ${RC} exprs`
  4. Verify: `dev/release/verify_rc.sh ${VERSION} ${RC} exprs`

The source archive is `apache-arrow-go-exprs-${VERSION}.tar.gz` and
contains only the module. Tags are prefixed with the module's directory,
for example `arrow/compute/exprs/v1.0.0-rc1` and `arrow/compute/exprs/v1.0.0`,
which is how Go finds versions of a module in a subdirectory.

The module depends on a released version of Arrow Go. To use unreleased
Arrow Go changes in `arrow/compute/exprs`, release Arrow Go first and
then update the requirement in `arrow/compute/exprs/go.mod`.
