This pass improves the admin architecture in three ways:

1. Moved admin forms into admin-specific partials
- create_user_form
- membership_upsert_form

2. Added a generic entity table card pattern
- entity_table_card
- used with typed inner partials:
  - user_table_typed
  - membership_table_typed
  - tenant_table_typed

3. Standardized destructive actions
- confirm_destructive_action partial
- JavaScript confirmation hook in base template

Outcome:
- admin screens are cleaner
- repeated admin interactions are centralized
- destructive actions now follow one reusable confirmation pattern
