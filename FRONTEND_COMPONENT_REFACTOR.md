This pass converts repeated UI patterns into reusable Go template partials/components.

Added:
- web/templates/components.html
  - page_hero
  - card_header
  - flash_messages
  - empty_state
  - metric_card
  - soft_list_item

Updated:
- internal/app/templates.go
  - added template helper funcs:
    - dict
    - slice

Refactored screens:
- login
- dashboard
- tenders
- queue
- password
- MFA
- MFA setup
- saved searches
- admin users
- admin tenants

Result:
- less repeated markup
- more consistent page structure
- reusable server-rendered UI building blocks
- easier future cleanup into a fuller component library
