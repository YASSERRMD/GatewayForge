# GatewayForge UI Design System

GatewayForge uses a neutral enterprise design language built with Tailwind CSS v4 CSS custom properties and accessible React primitives in `apps/web/src/components/ui/`.

## Color & Surface Tokens (`globals.css`)

- `--background` (`#f8fafc`) — primary canvas
- `--card` (`#ffffff`) — elevated content cards and tables
- `--surface-subtle` (`#f1f5f9`) — table headers, secondary surfaces
- `--foreground` (`#0f172a`) — primary high-contrast text (WCAG AAA on white)
- `--muted-foreground` (`#475569`) — secondary metadata labels (WCAG AA >= 7:1 on white)
- `--primary` (`#0f172a`) — primary action emphasis
- `--accent` (`#0284c7`) — focus rings (`--ring`), active nav, chart primary series
- `--success` (`#047857`), `--warning` (`#b45309`), `--destructive` (`#b91c1c`) — semantic statuses

## Core Primitives

- `Button` (`default`, `outline`, `ghost`, `destructive`; sizes `sm`, `default`, `lg`)
- `Badge` (`default`, `success`, `warning`, `destructive`)
- `Card`, `CardHeader`, `CardTitle`, `CardDescription`, `CardContent`
- `Label`, `Input`, `Textarea`, `Select`, `FieldError`
- `Dialog` (keyboard `Escape` dismissal, focus restoration, `role="dialog"`, `aria-modal="true"`)
- `Table`, `TableHeader`, `TableBody`, `TableRow`, `TableHead`, `TableCell`
- `Skeleton`, `EmptyState`, `ErrorBanner` (with retry hook)

## Accessibility Standards

- Every interactive control supports visible keyboard focus (`focus-visible:ring-2`).
- Tabular numeric metrics use `.tabular-nums` to avoid layout jitter during updates.
- Forms associate `<Label htmlFor>` with inputs and announce errors via `role="alert"`.
