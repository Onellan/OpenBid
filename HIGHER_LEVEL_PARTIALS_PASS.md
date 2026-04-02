This pass standardizes remaining repeated interaction patterns into higher-level reusable partials.

Added reusable interaction partials:
- queue_table
- saved_search_list
- tenant_table
- user_table
- membership_table
- saved_search_form
- tenant_form

Extended higher-level pattern partials:
- auth_form_shell
- filter_panel
- bulk_action_panel
- data_table_card
- form_actions
- status_badge

Added template helper:
- condTone

Refactored screens:
- queue
- saved searches
- admin tenants
- admin users
- password
- MFA
- MFA setup

Result:
The app now has fewer repeated interaction blocks and a more scalable server-rendered component architecture.
