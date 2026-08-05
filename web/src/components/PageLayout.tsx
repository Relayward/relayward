import type { ReactNode } from "react"
import { Network } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Card, CardAction, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card"
import { cn } from "@/lib/utils"

export function BrandMark({ compact = false }: { compact?: boolean }) {
  return (
    <span className="inline-flex min-w-0 items-center gap-3">
      <span className="flex aspect-square size-8 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground">
        <Network className="size-4" />
      </span>
      {!compact ? (
        <span className="grid min-w-0 text-left text-sm leading-tight">
          <strong className="truncate font-medium">Relayward</strong>
          <small className="truncate text-xs text-muted-foreground">Control Plane</small>
        </span>
      ) : null}
    </span>
  )
}

export function PageHeader({ id, eyebrow, title, description, actions }: {
  id: string
  eyebrow?: string
  title: string
  description?: string
  actions?: ReactNode
}) {
  return (
    <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
      <div className="min-w-0 space-y-2">
        {eyebrow ? <p className="m-0 text-sm font-medium text-muted-foreground">{eyebrow}</p> : null}
        <h1 className="m-0 text-2xl font-bold tracking-tight" id={id}>{title}</h1>
        {description ? <p className="m-0 max-w-3xl text-sm text-muted-foreground">{description}</p> : null}
      </div>
      {actions ? <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  )
}

export function SummaryBar({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn(
      "mb-6 grid gap-4 sm:grid-cols-2 xl:grid-cols-4 *:data-[slot=card]:bg-gradient-to-t *:data-[slot=card]:from-primary/5 *:data-[slot=card]:to-card *:data-[slot=card]:shadow-xs dark:*:data-[slot=card]:bg-card",
      className,
    )}>
      {children}
    </div>
  )
}

export function SummaryItem({ label, value, note, icon, tone = "default" }: {
  label: string
  value: ReactNode
  note?: ReactNode
  icon?: ReactNode
  tone?: "default" | "primary" | "success" | "warning" | "danger"
}) {
  const toneClass = {
    default: "text-muted-foreground",
    primary: "text-foreground",
    success: "text-success-strong",
    warning: "text-warning-strong",
    danger: "text-destructive",
  }[tone]

  return (
    <Card className="@container/card">
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle className="text-2xl font-semibold tabular-nums @[250px]/card:text-3xl">{value}</CardTitle>
        {icon ? <CardAction><Badge variant="outline" className={toneClass}>{icon}</Badge></CardAction> : null}
      </CardHeader>
      {note ? <CardFooter className="flex-col items-start gap-1.5 text-sm text-muted-foreground">{note}</CardFooter> : null}
    </Card>
  )
}

export function StatusBadge({ children, tone = "muted", dot = true, className }: {
  children: ReactNode
  tone?: "success" | "warning" | "danger" | "info" | "muted"
  dot?: boolean
  className?: string
}) {
  const toneClass = {
    success: "text-success-strong",
    warning: "text-warning-strong",
    danger: "text-destructive",
    info: "text-foreground",
    muted: "text-muted-foreground",
  }[tone]
  const dotClass = {
    success: "bg-success",
    warning: "bg-warning",
    danger: "bg-destructive",
    info: "bg-foreground",
    muted: "bg-muted-foreground",
  }[tone]

  return (
    <Badge variant="outline" className={cn(toneClass, className)}>
      {dot ? <span className={cn("size-1.5 shrink-0 rounded-full", dotClass)} /> : null}
      <span className="truncate">{children}</span>
    </Badge>
  )
}
