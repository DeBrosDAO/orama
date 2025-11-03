#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NOCOLOR='\033[0m'

log() { echo -e "${CYAN}[update-changelog]${NOCOLOR} $1"; }
error() { echo -e "${RED}[ERROR]${NOCOLOR} $1"; }
success() { echo -e "${GREEN}[SUCCESS]${NOCOLOR} $1"; }
warning() { echo -e "${YELLOW}[WARNING]${NOCOLOR} $1"; }

# File paths
CHANGELOG_FILE="CHANGELOG.md"
MAKEFILE="Makefile"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$REPO_ROOT"

# Load environment variables from .env file if it exists
if [ -f "$REPO_ROOT/.env" ]; then
    # Export variables from .env file (more portable than source <())
    set -a
    while IFS='=' read -r key value; do
        # Skip comments and empty lines
        [[ "$key" =~ ^#.*$ ]] && continue
        [[ -z "$key" ]] && continue
        # Remove quotes if present
        value=$(echo "$value" | sed -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//")
        export "$key=$value"
    done < "$REPO_ROOT/.env"
    set +a
fi

# OpenRouter API key
# Priority: 1. Environment variable, 2. .env file, 3. Exit with error
if [ -z "$OPENROUTER_API_KEY" ]; then
    error "OPENROUTER_API_KEY not found!"
    echo ""
    echo "Please set the API key in one of these ways:"
    echo "  1. Create a .env file in the repo root with:"
    echo "     OPENROUTER_API_KEY=your-api-key-here"
    echo ""
    echo "  2. Set it as an environment variable:"
    echo "     export OPENROUTER_API_KEY=your-api-key-here"
    echo ""
    echo "  3. Copy .env.example to .env and fill in your key:"
    echo "     cp .env.example .env"
    echo ""
    echo "Get your API key from: https://openrouter.ai/keys"
    exit 1
fi

# Check dependencies
if ! command -v jq > /dev/null 2>&1; then
    error "jq is required but not installed. Install it with: brew install jq (macOS) or apt-get install jq (Linux)"
    exit 1
fi

if ! command -v curl > /dev/null 2>&1; then
    error "curl is required but not installed"
    exit 1
fi

# Check if we're in a git repo
if ! git rev-parse --git-dir > /dev/null 2>&1; then
    error "Not in a git repository"
    exit 1
fi

# Get current branch
CURRENT_BRANCH=$(git rev-parse --abbrev-ref HEAD)
REMOTE_BRANCH="origin/$CURRENT_BRANCH"

# Check if remote branch exists
if ! git rev-parse --verify "$REMOTE_BRANCH" > /dev/null 2>&1; then
    warning "Remote branch $REMOTE_BRANCH does not exist. Using main/master as baseline."
    if git rev-parse --verify "origin/main" > /dev/null 2>&1; then
        REMOTE_BRANCH="origin/main"
    elif git rev-parse --verify "origin/master" > /dev/null 2>&1; then
        REMOTE_BRANCH="origin/master"
    else
        warning "No remote branch found. Using HEAD as baseline."
        REMOTE_BRANCH="HEAD"
    fi
fi

# Gather all git diffs
log "Collecting git diffs..."

# Unstaged changes
UNSTAGED_DIFF=$(git diff 2>/dev/null || echo "")
UNSTAGED_COUNT=$(echo "$UNSTAGED_DIFF" | grep -c "^diff\|^index" 2>/dev/null || echo "0")
[ -z "$UNSTAGED_COUNT" ] && UNSTAGED_COUNT="0"

# Staged changes
STAGED_DIFF=$(git diff --cached 2>/dev/null || echo "")
STAGED_COUNT=$(echo "$STAGED_DIFF" | grep -c "^diff\|^index" 2>/dev/null || echo "0")
[ -z "$STAGED_COUNT" ] && STAGED_COUNT="0"

# Unpushed commits
UNPUSHED_DIFF=$(git diff "$REMOTE_BRANCH"..HEAD 2>/dev/null || echo "")
UNPUSHED_COMMITS=$(git rev-list --count "$REMOTE_BRANCH"..HEAD 2>/dev/null || echo "0")
[ -z "$UNPUSHED_COMMITS" ] && UNPUSHED_COMMITS="0"

log "Found: $UNSTAGED_COUNT unstaged file(s), $STAGED_COUNT staged file(s), $UNPUSHED_COMMITS unpushed commit(s)"

# Combine all diffs
ALL_DIFFS="${UNSTAGED_DIFF}
---
STAGED CHANGES:
---
${STAGED_DIFF}
---
UNPUSHED COMMITS:
---
${UNPUSHED_DIFF}"

# Check if there are any changes
if [ -z "$(echo "$UNSTAGED_DIFF$STAGED_DIFF$UNPUSHED_DIFF" | tr -d '[:space:]')" ]; then
    log "No changes detected (unstaged, staged, or unpushed). Skipping changelog update."
    # Clean up any old preview files
    rm -f "$REPO_ROOT/.changelog_preview.tmp" "$REPO_ROOT/.changelog_version.tmp"
    exit 0
fi

# Get current version from Makefile
CURRENT_VERSION=$(grep "^VERSION :=" "$MAKEFILE" | sed 's/.*:= *//' | tr -d ' ')

if [ -z "$CURRENT_VERSION" ]; then
    error "Could not find VERSION in Makefile"
    exit 1
fi

log "Current version: $CURRENT_VERSION"

# Prepare prompt for OpenRouter
PROMPT="You are analyzing git diffs to create a changelog entry. Based on the following git diffs, create a simple, easy-to-understand changelog entry.

Current version: $CURRENT_VERSION

Git diffs:
\`\`\`
$ALL_DIFFS
\`\`\`

Please respond with ONLY a valid JSON object in this exact format:
{
  \"version\": \"x.y.z\",
  \"bump_type\": \"minor\" or \"patch\",
  \"date\": \"YYYY-MM-DD\",
  \"added\": [\"item1\", \"item2\"],
  \"changed\": [\"item1\", \"item2\"],
  \"fixed\": [\"item1\", \"item2\"]
}

Rules:
- Bump version based on changes: use \"minor\" for new features, \"patch\" for bug fixes and small changes
- Never bump major version (keep major version the same)
- Keep descriptions simple and easy to understand (1-2 sentences max per item)
- Only include items that actually changed
- If a category is empty, use an empty array []
- Date should be today's date in YYYY-MM-DD format"

# Call OpenRouter API
log "Calling OpenRouter API to generate changelog..."

# Prepare the JSON payload properly
PROMPT_ESCAPED=$(echo "$PROMPT" | jq -Rs .)
REQUEST_BODY=$(cat <<EOF
{
  "model": "google/gemini-2.5-flash-preview-09-2025",
  "messages": [
    {
      "role": "user",
      "content": $PROMPT_ESCAPED
    }
  ],
  "temperature": 0.3
}
EOF
)

# Debug: Check API key format (first 10 chars only)
API_KEY_PREFIX="${OPENROUTER_API_KEY:0:10}..."
log "Using API key: $API_KEY_PREFIX (length: ${#OPENROUTER_API_KEY})"

set +e  # Temporarily disable exit on error to check curl response
RESPONSE=$(curl -s -w "\nHTTP_CODE:%{http_code}" -X POST "https://openrouter.ai/api/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $OPENROUTER_API_KEY" \
  -d "$REQUEST_BODY")
CURL_EXIT_CODE=$?

# Extract HTTP code and response body
HTTP_CODE=$(echo "$RESPONSE" | grep -o "HTTP_CODE:[0-9]*" | cut -d: -f2)
RESPONSE_BODY=$(echo "$RESPONSE" | sed '/HTTP_CODE:/d')

set -e  # Re-enable exit on error

log "HTTP Status Code: $HTTP_CODE"

# Check if API call succeeded
if [ $CURL_EXIT_CODE -ne 0 ] || [ -z "$RESPONSE_BODY" ]; then
    error "Failed to call OpenRouter API"
    if [ $CURL_EXIT_CODE -ne 0 ]; then
        echo "Network error (curl exit code: $CURL_EXIT_CODE)"
    else
        echo "Empty response from API"
    fi
    exit 1
fi

# Check for API errors in response
if echo "$RESPONSE_BODY" | jq -e '.error' > /dev/null 2>&1; then
    error "OpenRouter API error:"
    ERROR_MESSAGE=$(echo "$RESPONSE_BODY" | jq -r '.error.message // .error' 2>/dev/null || echo "$RESPONSE_BODY")
    echo "$ERROR_MESSAGE"
    echo ""
    error "Full API response:"
    echo "$RESPONSE_BODY" | jq '.' 2>/dev/null || echo "$RESPONSE_BODY"
    echo ""
    error "The API key may be invalid or expired. Please verify your OpenRouter API key at https://openrouter.ai/keys"
    echo ""
    error "To test your API key manually, run:"
    echo "  curl https://openrouter.ai/api/v1/chat/completions \\"
    echo "    -H \"Content-Type: application/json\" \\"
    echo "    -H \"Authorization: Bearer YOUR_API_KEY\" \\"
    echo "    -d '{\"model\": \"google/gemini-2.5-flash-preview-09-2025\", \"messages\": [{\"role\": \"user\", \"content\": \"test\"}]}'"
    exit 1
fi

# Extract JSON from response
JSON_CONTENT=$(echo "$RESPONSE_BODY" | jq -r '.choices[0].message.content' 2>/dev/null)

# Check if content was extracted
if [ -z "$JSON_CONTENT" ] || [ "$JSON_CONTENT" = "null" ]; then
    error "Failed to extract content from API response"
    echo "Response: $RESPONSE_BODY"
    exit 1
fi

# Try to extract JSON if it's wrapped in markdown code blocks
if echo "$JSON_CONTENT" | grep -q '```json'; then
    JSON_CONTENT=$(echo "$JSON_CONTENT" | sed -n '/```json/,/```/p' | sed '1d;$d')
elif echo "$JSON_CONTENT" | grep -q '```'; then
    JSON_CONTENT=$(echo "$JSON_CONTENT" | sed -n '/```/,/```/p' | sed '1d;$d')
fi

# Validate JSON
if ! echo "$JSON_CONTENT" | jq . > /dev/null 2>&1; then
    error "Invalid JSON response from API:"
    echo "$JSON_CONTENT"
    exit 1
fi

# Parse JSON
NEW_VERSION=$(echo "$JSON_CONTENT" | jq -r '.version')
BUMP_TYPE=$(echo "$JSON_CONTENT" | jq -r '.bump_type')
DATE=$(echo "$JSON_CONTENT" | jq -r '.date')
ADDED=$(echo "$JSON_CONTENT" | jq -r '.added[]?' | sed 's/^/- /')
CHANGED=$(echo "$JSON_CONTENT" | jq -r '.changed[]?' | sed 's/^/- /')
FIXED=$(echo "$JSON_CONTENT" | jq -r '.fixed[]?' | sed 's/^/- /')

log "Generated version: $NEW_VERSION ($BUMP_TYPE bump)"
log "Date: $DATE"

# Validate version format
if ! echo "$NEW_VERSION" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    error "Invalid version format: $NEW_VERSION"
    exit 1
fi

# Validate date format
if ! echo "$DATE" | grep -qE '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'; then
    error "Invalid date format: $DATE (expected YYYY-MM-DD)"
    exit 1
fi

# Validate bump type
if [ "$BUMP_TYPE" != "minor" ] && [ "$BUMP_TYPE" != "patch" ]; then
    error "Invalid bump type: $BUMP_TYPE (must be 'minor' or 'patch')"
    exit 1
fi

# Update Makefile
log "Updating Makefile..."
if [[ "$OSTYPE" == "darwin"* ]]; then
    # macOS sed requires backup extension
    sed -i '' "s/^VERSION := .*/VERSION := $NEW_VERSION/" "$MAKEFILE"
else
    # Linux sed
    sed -i "s/^VERSION := .*/VERSION := $NEW_VERSION/" "$MAKEFILE"
fi
success "Makefile updated to version $NEW_VERSION"

# Update CHANGELOG.md
log "Updating CHANGELOG.md..."

# Create changelog entry
CHANGELOG_ENTRY="## [$NEW_VERSION] - $DATE

### Added
"
if [ -n "$ADDED" ]; then
    CHANGELOG_ENTRY+="$ADDED"$'\n'
else
    CHANGELOG_ENTRY+="\n"
fi

CHANGELOG_ENTRY+="
### Changed
"
if [ -n "$CHANGED" ]; then
    CHANGELOG_ENTRY+="$CHANGED"$'\n'
else
    CHANGELOG_ENTRY+="\n"
fi

CHANGELOG_ENTRY+="
### Deprecated

### Removed

### Fixed
"
if [ -n "$FIXED" ]; then
    CHANGELOG_ENTRY+="$FIXED"$'\n'
else
    CHANGELOG_ENTRY+="\n"
fi

CHANGELOG_ENTRY+="
"

# Save preview to temp file for pre-push hook
PREVIEW_FILE="$REPO_ROOT/.changelog_preview.tmp"
echo "$CHANGELOG_ENTRY" > "$PREVIEW_FILE"
echo "$NEW_VERSION" > "$REPO_ROOT/.changelog_version.tmp"

# Insert after [Unreleased] section using awk (more portable)
# Find the line number after [Unreleased] section (after the "### Fixed" line)
INSERT_LINE=$(awk '/^## \[Unreleased\]/{found=1} found && /^### Fixed$/{print NR+1; exit}' "$CHANGELOG_FILE")

if [ -z "$INSERT_LINE" ]; then
    # Fallback: insert after line 16 (after [Unreleased] section)
    INSERT_LINE=16
fi

# Use a temp file approach to insert multiline content
TMP_FILE=$(mktemp)
{
    head -n $((INSERT_LINE - 1)) "$CHANGELOG_FILE"
    printf '%s' "$CHANGELOG_ENTRY"
    tail -n +$INSERT_LINE "$CHANGELOG_FILE"
} > "$TMP_FILE"
mv "$TMP_FILE" "$CHANGELOG_FILE"

success "CHANGELOG.md updated with version $NEW_VERSION"

log "Changelog update complete!"
log "New version: $NEW_VERSION"
log "Bump type: $BUMP_TYPE"

