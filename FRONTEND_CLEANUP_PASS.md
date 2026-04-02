# OpenBid frontend cleanup pass

This pass focused on turning the branded templates into a more coherent product UI.

## What changed
- Consolidated visual system into a cleaner base template
- Reduced inline styling and replaced it with reusable layout/component classes
- Added consistent page hero pattern across key screens
- Standardized cards, metric tiles, empty states, tables, forms, toolbars, and split layouts
- Improved hierarchy, spacing, readability, and mobile responsiveness
- Reworked key screens:
  - login
  - dashboard
  - opportunities
  - pipeline
  - password
  - MFA
  - saved searches
  - user admin
  - tenant admin

## Intent
This is still server-rendered Go templates, but it now behaves more like one cohesive SaaS interface rather than a collection of individually styled pages.

## Recommended next frontend step
Move the current design system into:
- reusable template partials, or
- a component-based frontend layer

The current pass makes that transition much cleaner because the visual language is now far more consistent.
