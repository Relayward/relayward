import * as React from "react"
import * as TabsPrimitive from "@radix-ui/react-tabs"

import { cn } from "@/lib/utils"

function Tabs({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Root>) {
  return (
    <TabsPrimitive.Root
      data-slot="tabs"
      className={cn("flex min-w-0 flex-col gap-2", className)}
      {...props}
    />
  )
}

function TabsList({
  className,
  variant = "default",
  ...props
}: React.ComponentProps<typeof TabsPrimitive.List> & { variant?: "default" | "underline" }) {
  return (
    <TabsPrimitive.List
      data-slot="tabs-list"
      className={cn(
        "text-muted-foreground inline-flex items-center",
        variant === "default" && "h-9 w-fit justify-center rounded-lg bg-muted p-[3px]",
        variant === "underline" && "h-auto w-full justify-start gap-4 overflow-x-auto rounded-none border-b bg-transparent p-0",
        className
      )}
      {...props}
    />
  )
}

function TabsTrigger({
  className,
  variant = "default",
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Trigger> & { variant?: "default" | "underline" }) {
  return (
    <TabsPrimitive.Trigger
      data-slot="tabs-trigger"
      className={cn(
        "focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:outline-ring inline-flex cursor-pointer items-center justify-center gap-1.5 text-sm font-medium whitespace-nowrap focus-visible:ring-[3px] focus-visible:outline-1 disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
        variant === "default" && "h-[calc(100%-1px)] flex-1 rounded-md border border-transparent px-2 py-1 text-foreground transition-[color,box-shadow] data-[state=active]:bg-primary-soft data-[state=active]:text-primary-strong data-[state=active]:shadow-sm dark:text-muted-foreground dark:data-[state=active]:text-primary-strong",
        variant === "underline" && "relative h-11 flex-none rounded-none border-0 bg-transparent px-0 py-3 text-muted-foreground shadow-none transition-colors after:absolute after:inset-x-0 after:bottom-0 after:h-0.5 after:scale-x-0 after:bg-primary after:transition-transform hover:text-foreground data-[state=active]:bg-transparent data-[state=active]:text-primary-strong data-[state=active]:shadow-none data-[state=active]:after:scale-x-100 dark:data-[state=active]:text-primary",
        className
      )}
      {...props}
    />
  )
}

function TabsContent({
  className,
  ...props
}: React.ComponentProps<typeof TabsPrimitive.Content>) {
  return (
    <TabsPrimitive.Content
      data-slot="tabs-content"
      className={cn("flex-1 outline-none", className)}
      {...props}
    />
  )
}

export { Tabs, TabsList, TabsTrigger, TabsContent }
