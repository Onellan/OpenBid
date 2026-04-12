# OpenBid Design System

## Purpose

This design system formalizes the current server-rendered OpenBid UI into a reusable, maintainable set of rules and components.

It is designed for:

- consistency across screens
- easier future maintenance
- clearer handoff to designers and frontend engineers
- a stable bridge to any future componentized frontend stack

---

## 1. Foundations

### 1.1 Design principles

1. Clarity over decoration
2. Professional confidence
3. Readability at speed
4. Structured workflows
5. Calm, high-trust interaction patterns
6. Light-theme-first enterprise usability

### 1.2 Tokens

All visual decisions should come from the tokens defined in `web/templates/base.html`.

#### Color tokens

- `--color-primary`
- `--color-primary-strong`
- `--color-primary-soft`
- `--color-secondary`
- `--color-accent`
- `--color-bg`
- `--color-surface`
- `--color-surface-soft`
- `--color-text`
- `--color-text-muted`
- `--color-border`
- `--color-success`
- `--color-warning`
- `--color-danger`
- `--color-info`

#### Radius tokens

- `--radius-card`
- `--radius-control`
- `--radius-badge`

#### Shadow tokens

- `--shadow-soft`
- `--shadow-strong`

#### Spacing scale

Recommended spacing scale:

- 4
- 8
- 12
- 16
- 20
- 24
- 32
- 40
- 48

Use these consistently for padding, gaps, vertical rhythm, and layout spacing.

---

## 2. Typography

### Heading hierarchy

- `.page-title` — top-level page hero heading
- `.section-title` — section headline
- `.card-title` — card heading
- `.card-subtitle` — supporting description

### Supporting text

- `.muted`
- `.footer-note`
- `.small`
- `.mono`

### Typography rules

- Use strong hierarchy, not excessive font variation
- Keep headings concise
- Use supporting copy below headings for explanation
- Avoid dense paragraph blocks inside tables or admin screens

---

## 3. Layout primitives

### Main layout classes

- `.shell`
- `.grid`
- `.split`
- `.stack`
- `.cards`
- `.kpi-band`
- `.row-4`
- `.form-grid`
- `.table-wrap`

### Usage

- Use `.split` for balanced 2-column layouts
- Use `.stack` for vertically grouped cards
- Use `.cards` for repeated summary cards
- Use `.kpi-band` for dashboard metric cards
- Use `.row-4` for multi-control form rows
- Use `.form-grid` for consistent form spacing

---

## 4. Surface system

### Surface components

- `.card`
- `.card-soft`
- `.page-hero`

### Rules

- `.page-hero` is for page introductions and top-level messaging
- `.card` is for core UI containers
- `.card-soft` is for internal sub-sections, notes, and lightweight grouped content

---

## 5. Buttons and actions

### Button variants

- `.button` — primary action
- `.button.secondary` — secondary action

### Rules

- Primary actions should be limited to the main next step
- Secondary actions should support, not compete with, the primary action
- Avoid more than 2–3 visually equal actions in the same zone

---

## 6. Status and messaging

### Badge system

- `.badge`
- `.badge.success`
- `.badge.warning`
- `.badge.danger`
- `.badge.info`

### Message component

Use the shared partial:

- `flash_messages`

### Rules

- Use success only for confirmed positive outcomes
- Use warning for in-progress, pending, or caution states
- Use danger for failure, blocked, or invalid states
- Use info for neutral status or contextual emphasis

---

## 7. Data display patterns

### Table pattern

- card container
- `card_header`
- `table-wrap`
- standard table
- empty state row

### KPI pattern

- `metric_card`
- `.kpi-band`

### Soft list pattern

- `soft_list_item`

### Rules

- Use tables for scanning
- Use cards for explanation and action
- Use soft list items for metadata-heavy summaries

---

## 8. Form patterns

### Standard form structure

1. `card_header`
2. optional `flash_messages`
3. `.form-grid`
4. grouped fields with `.row-4` where appropriate
5. single clear submission button

### Rules

- Labels above controls
- One primary action per form
- Avoid mixing destructive actions into the same form block
- Keep destructive actions separate and visually clearer

---

## 9. Navigation patterns

### Header navigation

Top-level navigation should remain:

- Dashboard
- Opportunities
- Pipeline
- Saved Searches
- Password
- MFA
- Users
- Tenants
- Logout

### Tenant switching

Tenant switching remains a utility action grouped with global navigation rather than a primary page action.

---

## 10. Reusable template partials

Current shared partials:

- `page_hero`
- `card_header`
- `flash_messages`
- `empty_state`
- `metric_card`
- `soft_list_item`

### Rules for new partials

Every new repeated pattern should become a partial if it appears in 3 or more places.

Recommended next partials:

- filter_panel
- bulk_action_panel
- data_table_card
- inline_form_actions
- status_badge_map
- section_shell
- auth_form_shell

---

## 11. Screen templates covered

The design system currently supports:

- login
- dashboard
- tenders / opportunities
- queue / pipeline
- password
- MFA
- MFA setup
- saved searches
- user admin
- tenant admin

---

## 12. State patterns

### Empty states

Use the `empty_state` partial with direct, helpful language.

### Success states

Use `flash_messages`.

### Error states

Use `flash_messages` with the `Error` binding.

### In-progress states

Use warning badges and contextual copy.

---

## 13. Future extension path

This system is intentionally built so it can evolve into:

- a server-rendered component library
- a React/Vue component mapping
- a Figma design system
- a frontend token package

---

## 14. Governance

When updating screens:

1. Use existing tokens first
2. Use existing partials before adding markup
3. Add new partials only for repeated patterns
4. Keep page structure consistent: hero → content cards → tables/lists/forms
5. Do not add one-off inline styling unless necessary
