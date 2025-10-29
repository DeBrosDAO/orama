#!/bin/bash

# DeBros Network Interactive Release Script
# Handles the complete release workflow for both stable and nightly releases

set -e

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BLUE='\033[38;2;2;128;175m'
YELLOW='\033[1;33m'
NOCOLOR='\033[0m'

log() { echo -e "${CYAN}[$(date '+%Y-%m-%d %H:%M:%S')]${NOCOLOR} $1"; }
error() { echo -e "${RED}[ERROR]${NOCOLOR} $1"; }
success() { echo -e "${GREEN}[SUCCESS]${NOCOLOR} $1"; }
warning() { echo -e "${YELLOW}[WARNING]${NOCOLOR} $1"; }
info() { echo -e "${BLUE}ℹ${NOCOLOR} $1"; }

display_banner() {
    echo -e "${BLUE}========================================================================${NOCOLOR}"
    echo -e "${CYAN}
    ____       ____                   _   _      _                      _
   |  _ \\  ___| __ ) _ __ ___  ___   | \\ | | ___| |___      _____  _ __| | __
   | | | |/ _ \\  _ \\|  __/ _ \\/ __|  |  \\| |/ _ \\ __\\ \\ /\\ / / _ \\|  __| |/ /
   | |_| |  __/ |_) | | | (_) \\__ \\  | |\\  |  __/ |_ \\ V  V / (_) | |  |   <
   |____/ \\___|____/|_|  \\___/|___/  |_| \\_|\\___|\\__| \\_/\\_/ \\___/|_|  |_|\\_\\
${NOCOLOR}"
    echo -e "${BLUE}========================================================================${NOCOLOR}"
    echo -e "${GREEN}                      Release Management Tool${NOCOLOR}"
    echo -e "${BLUE}========================================================================${NOCOLOR}"
}

check_git_clean() {
    if ! git diff-index --quiet HEAD --; then
        error "Working directory has uncommitted changes. Please commit or stash them first."
        exit 1
    fi
}

check_current_branch() {
    CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
    echo "$CURRENT_BRANCH"
}

get_latest_version() {
    git tag --list 'v*' --sort=-version:refname | head -1 | sed 's/^v//' || echo "0.0.0"
}

increment_version() {
    local version=$1
    local bump=$2
    
    IFS='.' read -r major minor patch <<< "$version"
    
    case $bump in
        major)
            major=$((major + 1))
            minor=0
            patch=0
            ;;
        minor)
            minor=$((minor + 1))
            patch=0
            ;;
        patch)
            patch=$((patch + 1))
            ;;
    esac
    
    echo "$major.$minor.$patch"
}

prompt_release_type() {
    echo "" >&2
    echo -e "${CYAN}=== Release Type ===${NOCOLOR}" >&2
    echo "1) Stable Release (merge nightly → main, tag on main)" >&2
    echo "2) Nightly Release (tag directly on nightly)" >&2
    echo "3) Exit" >&2
    echo "" >&2
    read -p "Choose release type (1-3): " release_type >&2
    echo "$release_type"
}

prompt_version_strategy() {
    echo "" >&2
    echo -e "${CYAN}=== Version Strategy ===${NOCOLOR}" >&2
    local latest=$(get_latest_version)
    echo "Latest version: $latest" >&2
    echo "" >&2
    echo "1) Major bump ($latest → $(increment_version $latest major))" >&2
    echo "2) Minor bump ($latest → $(increment_version $latest minor))" >&2
    echo "3) Patch bump ($latest → $(increment_version $latest patch))" >&2
    echo "4) Custom version" >&2
    echo "" >&2
    read -p "Choose version strategy (1-4): " version_strategy >&2
    echo "$version_strategy"
}

prompt_custom_version() {
    read -p "Enter custom version (e.g., 0.52.1): " custom_version >&2
    if [[ ! $custom_version =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        error "Invalid version format. Must be X.Y.Z"
        exit 1
    fi
    echo "$custom_version"
}

confirm_release() {
    local version=$1
    local target=$2
    local msg=$3
    
    echo ""
    echo -e "${YELLOW}=== Release Summary ===${NOCOLOR}"
    echo "Version:     $version"
    echo "Target:      $target"
    echo "Message:     $msg"
    echo ""
    read -p "Is this correct? (yes/no): " confirm
    
    if [[ "$confirm" != "yes" ]]; then
        error "Release cancelled."
        exit 1
    fi
}

handle_stable_release() {
    local version=$1
    
    log "Stable Release: v$version"
    echo ""
    
    # Check if on nightly
    if [[ "$CURRENT_BRANCH" != "nightly" ]]; then
        warning "Not on nightly branch. Checking out nightly..."
        git checkout nightly
        git pull origin nightly
    else
        git pull origin nightly
    fi
    
    log "Current branch: nightly"
    info "Next step: Create PR from nightly → main in GitHub"
    info "Once PR is merged, this script will create the release tag"
    echo ""
    echo -e "${YELLOW}Have you already merged the PR to main? (yes/no)${NOCOLOR}"
    read -p "> " pr_merged
    
    if [[ "$pr_merged" != "yes" ]]; then
        warning "Please create and merge the PR first, then run this script again."
        exit 0
    fi
    
    # Verify main is updated
    log "Switching to main and pulling latest..."
    git checkout main
    git pull origin main
    
    # Create tag
    log "Creating tag v$version on main..."
    git tag -a "v$version" -m "Release v$version"
    
    success "Tag created: v$version"
}

handle_nightly_release() {
    local version=$1
    
    log "Nightly Release: v$version-nightly"
    echo ""
    
    # Check if on nightly
    if [[ "$CURRENT_BRANCH" != "nightly" ]]; then
        warning "Not on nightly branch. Checking out nightly..."
        git checkout nightly
        git pull origin nightly
    else
        git pull origin nightly
    fi
    
    log "Current branch: nightly"
    
    # Create tag
    log "Creating tag v$version-nightly on nightly..."
    git tag -a "v$version-nightly" -m "Nightly Release v$version"
    
    success "Tag created: v$version-nightly"
}

push_release() {
    local tag=$1
    
    echo ""
    echo -e "${CYAN}=== Pushing Release ===${NOCOLOR}"
    log "Pushing tag $tag to origin..."
    echo ""
    echo -e "${YELLOW}This will trigger GitHub Actions to build and publish the release.${NOCOLOR}"
    read -p "Continue? (yes/no): " confirm_push
    
    if [[ "$confirm_push" != "yes" ]]; then
        error "Push cancelled. Tag created but not pushed."
        exit 0
    fi
    
    git push origin "$tag"
    success "Tag pushed successfully!"
    
    echo ""
    echo -e "${GREEN}========================================================================${NOCOLOR}"
    echo -e "${GREEN}✅ Release Started!${NOCOLOR}"
    echo -e "${GREEN}========================================================================${NOCOLOR}"
    echo ""
    echo -e "📊 Monitor your release:"
    echo -e "  • GitHub Actions: https://github.com/DeBrosOfficial/network/actions"
    echo -e "  • Releases: https://github.com/DeBrosOfficial/network/releases"
    echo ""
    echo -e "⏱️  The build usually takes 2-5 minutes."
    echo -e "📦 Your release will appear on the Releases page once complete."
    echo ""
}

main() {
    display_banner
    
    # Check git status
    log "Checking git status..."
    check_git_clean
    
    CURRENT_BRANCH=$(check_current_branch)
    log "Current branch: $CURRENT_BRANCH"
    
    # Get release type
    release_type=$(prompt_release_type)
    
    if [[ "$release_type" == "3" ]]; then
        info "Release cancelled."
        exit 0
    fi
    
    # Get version strategy
    version_strategy=$(prompt_version_strategy)
    latest_version=$(get_latest_version)
    
    case $version_strategy in
        1)
            new_version=$(increment_version "$latest_version" major)
            ;;
        2)
            new_version=$(increment_version "$latest_version" minor)
            ;;
        3)
            new_version=$(increment_version "$latest_version" patch)
            ;;
        4)
            new_version=$(prompt_custom_version)
            ;;
        *)
            error "Invalid choice"
            exit 1
            ;;
    esac
    
    # Handle release based on type
    case $release_type in
        1)
            # Stable release
            confirm_release "$new_version" "main (stable)" "Release v$new_version to stable main branch"
            handle_stable_release "$new_version"
            push_release "v$new_version"
            ;;
        2)
            # Nightly release
            confirm_release "$new_version" "nightly (development)" "Release v$new_version-nightly to nightly branch"
            handle_nightly_release "$new_version"
            push_release "v$new_version-nightly"
            ;;
        *)
            error "Invalid choice"
            exit 1
            ;;
    esac
    
    echo -e "${GREEN}Done! 🎉${NOCOLOR}"
}

main "$@"
