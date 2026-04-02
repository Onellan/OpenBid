This pass removes brittle composition patterns and tightens template/runtime assumptions.

What was fixed:
- removed fake placeholder wrapper composition from the opportunities screen
- stopped relying on implicit root access inside opportunity-level partials
- changed opportunity-level partials to use explicit `{Root, Item}` payloads
- kept dynamic typed-table rendering only where the full page context is passed explicitly
- preserved generic patterns that are safe in plain Go templates

What was verified structurally:
- no route points to a missing handler in the current router/app files
- no duplicate template names were found outside the intentional shared `content` block pattern
- helper functions currently required by templates are:
  - dict
  - slice
  - condTone
- confirmation partials now receive explicit CSRF and hidden payload data
- opportunity typed partials no longer depend on fragile `$.` assumptions

Main brittle pattern removed:
- the opportunities screen previously rendered empty wrapper panels and then duplicated the real panels below them
- that has been simplified to one real rendering path
