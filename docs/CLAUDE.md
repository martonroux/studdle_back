# CLAUDE.md

This file provides global guidance to Claude Code when working on the Studdle backend.

> **Naming (2026-07-21, STU-34):** StudBud has been renamed **Studdle**. Both names refer to this exact codebase — there is no separate Studdle app. (A Next.js "Studdle" prototype briefly existed in the Paperclip agent workspace; it is deprecated, and all product development continues here.) The in-app rebrand landed via STU-35.

## Go Coding Standards

### KISS Principles (CRUCIAL)

- It is CRUCIAL to avoid over-engineering at all costs
- Keep everything simple: Keep It Simple, Stupid (KISS)
- Do not create unnecessary or premature abstractions
- Prefer explicit and straightforward code over "clever" code
- Do not add features "just in case" - only implement what is actually needed
- Avoid complex design patterns when a simple solution suffices
- The simplest code that works is often the best code

### Minimal Public API (CRUCIAL)

- Keep package APIs as small as possible - fewer public functions/methods/structs is better
- Start with the absolute minimum: if you only need 2 functions, expose only 2 functions
- Add new public API elements only when there is an actual, concrete need for them
- Do not expose functions "just in case" someone might need them later
- Prefer unexported (private) by default - only export what is strictly necessary
- A package with 3 well-designed public functions is better than one with 15 "convenient" ones
- Question every exported symbol: "Is this really needed by external code?"

### Documentation Requirements

- Every function, type, struct, and interface must have a docstring
- Every struct field must be documented with inline comments
- All code and documentation must be written in English
- Use Go's standard documentation format (start with the name being documented)
- Do NOT add docstrings before package declarations - Package declarations should not have documentation comments above them

### Code Style

- Maximize code readability with generous spacing
- Use descriptive variable and function names
- Follow Go naming conventions (PascalCase for exported, camelCase for unexported)

### Function Size (CRUCIAL)

- Functions MUST be short and focused - aim for 15-25 lines maximum
- A function that does multiple tasks MUST be split into smaller sub-functions
- If a function exceeds 30 lines, it is almost certainly doing too much
- Each function should have ONE clear responsibility
- Long functions (50+ lines) are NEVER acceptable - split them into well-named helper functions
- Prefer 3 small focused functions over 1 large function doing everything
- Good code reads like a story: the main function orchestrates, sub-functions execute specific tasks

### File Size (CRUCIAL)

- Files MUST stay focused and manageable - aim for 200-300 lines maximum
- A file that exceeds 400 lines is almost certainly doing too much and MUST be split
- Each file should have ONE clear responsibility or theme
- Split by logical domain: initialization, networking, sync, routing, etc.
- Prefer 4 small focused files over 1 large file doing everything
- File names should clearly describe their content (e.g., `sync.go`, `handlers.go`, `routing.go`)
- **Exception**: a file can exceed 400 lines if it represents a single cohesive type with tightly coupled methods (e.g., a core struct and all its methods). Only accept this when splitting would scatter related logic across files and hurt readability

### TODO Comments

- Use `// TODO:` comments to mark incomplete implementations or future improvements
- This helps avoid doing everything at once and prevents forgetting items for later
- Format: `// TODO: description of what needs to be done`
- Example: `// TODO: add validation for negative amounts`

### go.mod Hygiene (CRUCIAL)

- It is FORBIDDEN to commit or push code with a `replace` directive in `go.mod`
- `replace` directives are for local development ONLY
- Before any commit that touches `go.mod`, verify there are no `replace` lines present
- If a `replace` is needed locally, it must be removed before pushing

### Error Handling

- Always wrap errors with `fmt.Errorf` to add context about where/why the error occurred
- Use `%w` verb to preserve the original error for error unwrapping
- Include `\n` before `%w` for better readability in deep error stacks
- Never return raw errors without context - makes debugging much harder
- Format: `fmt.Errorf("operation description:\n%w", err)`
- Example: `return nil, fmt.Errorf("failed to read config file:\n%w", err)`

### Example Documentation Style

```go
package user

import (
	"context"
	"time"
)

// User represents a user entity in the system.
// It contains authentication and profile information.
type User struct {
	ID        int64     `json:"id"`         // ID is the unique identifier for the user
	Email     string    `json:"email"`      // Email is the user's email address used for authentication
	CreatedAt time.Time `json:"created_at"` // CreatedAt stores when the user account was created
}

// CreateUser creates a new user in the database.
// It validates the input and returns the created user with generated ID.
func CreateUser(ctx context.Context, input CreateUserInput) (*User, error) {
	// ...
}
```

### CLAUDE.md Design (CRUCIAL)

- CLAUDE.md files describe the **why** and **principles** — not the exact file tree or package list
- Do NOT put architecture diagrams with exact directory listings — they become stale after every refactor
- Describe architecture style (hexagonal, ports & adapters) and rules, not the current structure
- The code is its own documentation for structure — CLAUDE.md captures what is NOT visible in the code
- A good CLAUDE.md should rarely need updating; if it changes every commit, it's too specific

## Git Commit Convention

All commits in the StudBud repository follow this format:

### Title line

The first line is the commit title. It does NOT start with a prefix (`[+]`, `[&]`, etc.). It describes in a few words the purpose of the commit.

### Body

After a blank line, list the detailed changes using prefixes — one per line:

- **[+]** Feature addition
- **[-]** Feature removal
- **[&]** Changes, refactors, updates
- **[!]** Bug fixes

**Format**: One change per line, minimal words, maximum efficiency. List as many entries as needed — do not omit changes.

**Example**:
```
Bulk trade operations

[+] BulkAcceptTrades and BulkRejectTrades endpoints
[+] POST /wallets/pending-trades/bulk-accept
[+] POST /wallets/pending-trades/bulk-reject
[+] BulkTradeResult type with per-trade error handling
[+] 6 unit tests for bulk operations
```

**IMPORTANT**: NO footers, NO "🤖 Generated with...", NO "Co-Authored-By: Claude". Keep commits clean and minimal.

## Tooling

- **Go LSP (gopls)** is available — use it for go-to-definition, find-references, hover, call hierarchy, and symbol lookups across all Go modules
