# Release Process

This document describes how to create a new release of cache-warmer.

## Automatic Release with GitHub Actions

The GitHub Action (`.github/workflows/build.yml`) automatically creates a release when you push a version tag.

### Steps for a new release:

### 1. Ensure you're on the main/master branch

```bash
git checkout main
git pull origin main
```

### 2. Create a version tag

We use [Semantic Versioning](https://semver.org/): `MAJOR.MINOR.PATCH`

- **MAJOR**: Breaking changes (v2.0.0)
- **MINOR**: New features, backwards compatible (v1.1.0)
- **PATCH**: Bug fixes, backwards compatible (v1.0.1)

```bash
# Example for version 1.0.0
git tag -a v1.0.0 -m "Release v1.0.0: Initial Go implementation"

# Or for a patch release
git tag -a v1.0.1 -m "Release v1.0.1: Bug fixes"
```

### 3. Push the tag to GitHub

```bash
git push origin v1.0.0
```

### 4. GitHub Action does the rest! 🎉

The GitHub Action is automatically triggered and:

1. ✅ Builds binaries for Linux AMD64 and ARM64
2. ✅ Tests the binaries
3. ✅ Generates checksums
4. ✅ Creates a GitHub Release
5. ✅ Uploads binaries to the release
6. ✅ Adds release notes

### 5. View the release

Go to: `https://github.com/hpowernl/cache-warmer/releases`

The release is now publicly available with:
- Download links for both binaries
- Checksums for verification
- Installation instructions
- Automatically generated release notes

## Customizing Release Notes

The release is automatically created with default notes. To customize:

1. Go to the [Releases page](https://github.com/hpowernl/cache-warmer/releases)
2. Click "Edit" on the release
3. Update the description with:
   - New features
   - Bug fixes
   - Breaking changes
   - Upgrade instructions
4. Click "Update release"

## Example Release Notes Template

```markdown
## 🚀 What's New

- Added new feature X
- Performance improvement Y (30% faster)
- Bug fix Z

## 🔧 Changes

- Added config option A
- Changed default value for B

## ⚠️ Breaking Changes

(if applicable)
- Config parameter C renamed to D
- Migration: Run `cache-warmer migrate` after update

## 📦 Installation

See the [README](https://github.com/hpowernl/cache-warmer#-installation) for installation instructions.

## 🐛 Bug Fixes

- Fixed issue #123: ...
- Fixed issue #456: ...
```

## Creating a Pre-release

For test releases (beta, rc):

```bash
git tag -a v1.0.0-beta.1 -m "Beta release"
git push origin v1.0.0-beta.1
```

Then mark it as "pre-release" in GitHub.

## Deleting a Tag (if you make a mistake)

```bash
# Delete locally
git tag -d v1.0.0

# Delete from GitHub
git push origin :refs/tags/v1.0.0

# Also delete the release manually in GitHub UI
```

Then you can start over with the correct tag.

## Release Checklist

- [ ] Code is tested
- [ ] CHANGELOG.md is updated (optional)
- [ ] Version number follows Semantic Versioning
- [ ] Tag message is descriptive
- [ ] GitHub Action build succeeds
- [ ] Binaries are tested
- [ ] Release notes are customized (if needed)
- [ ] Installation is tested with new version

## Troubleshooting

### "GitHub Action fails during build"

- Check the Actions tab for error logs
- Test locally: `go build -o cache-warmer cache-warmer.go`
- Fix the issues and create a new patch version

### "Release is not created"

- Ensure the tag starts with `v` (e.g. `v1.0.0`, not `1.0.0`)
- Check if GITHUB_TOKEN permissions are correct (default should work)
- Look in the Actions logs for error messages

### "Binary doesn't work after download"

- Check if `chmod +x` was executed
- Verify checksum: `sha256sum -c checksums.txt`
- Test with `./cache-warmer --help`
