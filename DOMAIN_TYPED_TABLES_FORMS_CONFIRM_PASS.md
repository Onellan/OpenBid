This pass applies the typed table + form + confirmation pattern to more domains:

Added:
- saved_search_table_typed
- saved_search_form_typed
- pipeline_jobs_table_typed
- opportunity_danger_zone

Added handlers/routes:
- /queue/requeue
- /tenders/workflow/reset
- /tenders/bookmark/remove

Updated screens:
- saved searches now uses a typed table card and typed form
- pipeline jobs now uses a typed table card and reusable confirmation action
- opportunity cards now include a standardized destructive-action danger zone

Result:
The interaction architecture is more consistent across admin, saved searches, pipeline jobs, and opportunity-level destructive actions.
