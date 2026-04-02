This pass aggressively refactors the remaining opportunity interactions into higher-level reusable partials.

Added opportunity-specific partials:
- opportunity_filter_form
- opportunity_bulk_action_form
- opportunity_table
- opportunity_status_cluster
- opportunity_action_panel
- opportunity_card

Refactored:
- web/templates/tenders.html

Result:
The opportunities screen no longer hand-builds its filter form, bulk action form, table, status cluster, and action panel inline.
Those patterns now live as reusable template components for the rest of the app to adopt.
