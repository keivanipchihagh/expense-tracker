# Ideas

A backlog of features to pick from. Effort: **S** (an hour or two), **M** (a session), **L** (several sessions). ★ marks the strongest candidates.

Done so far: quick add from typed text and voice (the floating composer), receipt scanning, receipts kept with expenses, user-defined categories, export my data and delete account.

## Capture

- ★ **Install as a phone app (PWA)** — M. Home-screen icon that opens straight to a camera-ready Add expense, with an offline queue that syncs later.
- ★ **Telegram bot** — M. Send "12.50 lunch Rewe" or a receipt photo to a private bot; reuses text reading and the receipt scanner.
- **Forward receipts by email** — M. Forward an Amazon, Uber or Deliveroo receipt email to a personal address and it becomes an expense.
- **Apple Shortcuts / Siri** — S. "Hey Siri, log 4 euros coffee", as a shortcut calling the API.
- **Bank CSV import with presets** — M. Column mappings for N26, Revolut, DKB, ING and PayPal, with duplicate detection.
- **Automatic bank sync** — L. GoCardless Bank Account Data (formerly Nordigen) is free for personal use with EU banks; pull transactions daily for the user to confirm.

## Insight

- ★ **Ask your data** — M. A question box on Analysis ("eating out in October vs September?"); the model turns it into a filter and the server does the maths.
- **Personal inflation index** — M. Keep receipt line items and track what each item costs over time.
- **Subscription detector** — M. Find monthly repeating charges and list them, with price changes.
- **Unusual-spend alerts** — S/M. "This week's eating out is 2.3× your usual", against a rolling average.
- **Spending map** — M. Store a place per expense (read off receipts) and show where money goes.
- **Weekday and time-of-day heatmap** — S.
- **Merchant pages** — S. Total, visits, average spend and trend per merchant.
- **Year in review** — M. A December recap: biggest month, top merchants, most expensive day, streaks.
- **What-if calculator** — S. "If I halve takeaway, I save €X a year."

## Planning

- ★ **Budgets per category** — M. Monthly limit, progress bar, warning near the limit; agents can answer "how much food budget is left?".
- **Recurring expenses** — M. Rent and subscriptions created each month, optionally confirmed first.
- **Savings goals** — M. "Japan trip, €3,000 by June", with the monthly amount needed at the current pace.
- **No-spend days** — S. Calendar of days with no spending and a streak counter.
- **Envelope budgeting** — L. Split income into category pots each month and spend them down.

## Sharing

- ★ **Shared expenses** — L. Split a bill with another user (equal, shares or custom) and see who owes whom.
- **Household ledger** — L. A shared space for couples or flatmates next to private expenses.
- **Invite links** — S. Invite specific people instead of open sign-up.
- **Allowance mode** — M. Parent-managed kids' accounts with a weekly allowance.

## Receipts and paperwork

- **Warranty tracker** — S/M. Mark a receipt as a warranty with an end date; reminder before it expires.
- **Tax-deductible flag and export** — M. Tag work or tax-relevant expenses and export them yearly (e.g. Werbungskosten).
- **Paperless-ngx integration** — M. Send kept receipts to a document archive.
- **Attach a photo to any expense** — S. Without scanning: a PDF invoice, a photo, a statement.

## Integrations

- **Webhooks** — S/M. "Expense created" events to n8n, Zapier, Home Assistant or Discord.
- **Home Assistant sensor** — S. "Spent this month" on a dashboard.
- **ntfy / Gotify push notifications** — S. Budget warnings and weekly summaries.
- **Google Sheets sync** — M. A live copy of expenses in a spreadsheet.
- **Import from Splitwise, Firefly III or Actual Budget** — M.
- **Opt-in exchange rates** — M. Free daily ECB rates to convert foreign receipts at the purchase date's rate, per expense.

## Agents (MCP)

- ★ **`summarize_spending` tool** — S. Totals by category, merchant or month computed on the server.
- **MCP prompts** — S. "Monthly review", "Find subscriptions", "Budget check".
- **`scan_receipt` tool** — S. Send a receipt photo in a Claude chat and it's logged.
- **Weekly money coach** — M. A short Sunday summary by the free model, sent by email, Telegram or ntfy.

## Security and reliability

- ★ **Automatic backups** — S/M. Nightly `VACUUM INTO` (or Litestream to S3 or another server), keeping N copies; replaces manual `.bak` files.
- **Passkeys or two-factor login** — M.
- **Per-user API rate limit** — S. E.g. 120 requests a minute per token or session.
- **Active sessions list** — S. See signed-in browsers and sign one out.
- **Password reset** — S/M.

## Polish and quality

- ★ **Rename and merge categories** — S. Move all expenses of one category to another in one go (the gap left by user-defined categories).
- **Search across notes and merchants, plus tags** — M.
- **Undo after delete** — S. A 10-second Undo toast.
- **Keyboard shortcuts** — S. `n` new, `/` search, arrows for months.
- **German translation** — M.
- **OpenAPI spec and docs page** — S.
- **Browser tests in CI** — M. Sign-up, scan, add and categories in a real browser.

## Suggested order

1. Automatic backups and rename/merge categories.
2. Budgets and the `summarize_spending` tool.
3. Phone app (PWA) and the Telegram bot.
