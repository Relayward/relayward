import { useEffect, useState, type ComponentProps, type CSSProperties, type ReactNode } from "react"
import {
  Activity,
  BadgeCheck,
  CircleAlert,
  CircleUser,
  EllipsisVertical,
  LayoutDashboard,
  LogOut,
  Megaphone,
  Moon,
  Network,
  Plug,
  ScrollText,
  Search,
  Server,
  Settings,
  Shield,
  Sun,
  Users,
} from "lucide-react"

import type { SessionInfo } from "@/api"
import type { AdminView, PluginNavigationPage } from "@/adminNavigation"
import { LanguageSwitcher, useI18n } from "@/i18n"
import type { SystemInfo } from "@/system"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Input } from "@/components/ui/input"
import { InsecureTransportWarning } from "@/components/InsecureTransportWarning"
import { pluginIcons } from "@/pluginIcons"
import { Separator } from "@/components/ui/separator"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar"

export type { AdminView } from "@/adminNavigation"

interface AdminShellProps {
  view: AdminView
  onViewChange: (view: AdminView) => void
  session: SessionInfo
  system: SystemInfo
  busy: boolean
  onLogout: () => void
  pluginPages: PluginNavigationPage[]
  children: ReactNode
}

interface NavigationItem {
  view: AdminView
  label: string
  icon: typeof LayoutDashboard
  order: number
  translated?: boolean
  unavailable?: boolean
}

interface NavigationGroupDefinition {
  label: string
  pluginGroup?: PluginNavigationPage["group"]
  items: NavigationItem[]
}

const coreNavigation: NavigationGroupDefinition[] = [
  {
    label: "Overview",
    items: [{ view: "system", label: "System overview", icon: LayoutDashboard, order: 100 }],
  },
  {
    label: "Resource management",
    pluginGroup: "resources",
    items: [
      { view: "nodes", label: "Nodes", icon: Server, order: 100 },
      { view: "users", label: "Users", icon: Users, order: 200 },
      { view: "authorizations", label: "Authorizations", icon: BadgeCheck, order: 300 },
    ],
  },
  {
    label: "Extensions",
    pluginGroup: "extensions",
    items: [{ view: "plugins", label: "Plugins", icon: Plug, order: 100 }],
  },
  {
    label: "Observability",
    pluginGroup: "observability",
    items: [
      { view: "events", label: "Recent events", icon: Activity, order: 100 },
      { view: "audit", label: "Audit", icon: ScrollText, order: 200 },
    ],
  },
  {
    label: "System",
    items: [
      { view: "announcement", label: "Announcement", icon: Megaphone, order: 100 },
      { view: "settings", label: "Settings", icon: Settings, order: 200 },
      { view: "security", label: "Security", icon: Shield, order: 300 },
    ],
  },
]

function navigationWithPlugins(pluginPages: PluginNavigationPage[]): NavigationGroupDefinition[] {
  const result = coreNavigation.map((group) => ({ ...group, items: [...group.items] }))
  for (const page of pluginPages) {
    const group = result.find((candidate) => candidate.pluginGroup === page.group)
    if (group === undefined) continue
    group.items.push({
      view: page.view,
      label: page.label,
      icon: pluginIcons[page.icon],
      order: page.order,
      translated: false,
      unavailable: page.unavailable,
    })
  }
  for (const group of result) group.items.sort((left, right) => left.order - right.order || left.view.localeCompare(right.view))
  return result
}

function navigationLabel(item: NavigationItem, translate: (message: string) => string) {
  return item.translated === false ? item.label : translate(item.label)
}

export function AdminShell({ view, onViewChange, session, system, busy, onLogout, pluginPages, children }: AdminShellProps) {
  return (
    <SidebarProvider
      style={{
        "--sidebar-width": "16rem",
        "--sidebar-width-icon": "3rem",
        "--header-height": "calc(var(--spacing) * 14)",
      } as CSSProperties}
    >
      <AdminSidebar
        view={view}
        onViewChange={onViewChange}
        username={session.administrator.username}
        busy={busy}
        onLogout={onLogout}
        pluginPages={pluginPages}
        variant="inset"
        collapsible="icon"
      />
      <SidebarInset>
        <AdminHeader view={view} onViewChange={onViewChange} version={system.version} pluginPages={pluginPages} />
        <InsecureTransportWarning className="mx-4 mt-4 lg:mx-6" />
        <div className="flex flex-1 flex-col">
          <div className="@container/main flex flex-1 flex-col gap-2">
            <main className="flex flex-col gap-4 py-4 md:gap-6 md:py-6">
              {children}
            </main>
          </div>
        </div>
      </SidebarInset>
    </SidebarProvider>
  )
}

function AdminSidebar({ view, onViewChange, username, busy, onLogout, pluginPages, ...props }: ComponentProps<typeof Sidebar> & {
  view: AdminView
  onViewChange: (view: AdminView) => void
  username: string
  busy: boolean
  onLogout: () => void
  pluginPages: PluginNavigationPage[]
}) {
  const { t } = useI18n()
  const { isMobile, setOpenMobile } = useSidebar()
  const navigation = navigationWithPlugins(pluginPages)

  function navigate(nextView: AdminView) {
    onViewChange(nextView)
    if (isMobile) setOpenMobile(false)
  }

  return (
    <Sidebar {...props}>
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <SidebarMenuButton size="lg" onClick={() => navigate("system")} tooltip="Relayward">
              <div className="flex aspect-square size-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
                <Network className="size-4" />
              </div>
              <div className="grid flex-1 text-left text-sm leading-tight">
                <span className="truncate font-medium">Relayward</span>
                <span className="truncate text-xs">Control Plane</span>
              </div>
            </SidebarMenuButton>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>
      <SidebarContent>
        {navigation.map((group) => (
          <SidebarGroup key={group.label}>
            <SidebarGroupLabel>{t(group.label)}</SidebarGroupLabel>
            <SidebarMenu>
              {group.items.map((item) => (
                <SidebarMenuItem key={item.view}>
                  <SidebarMenuButton
                    isActive={view === item.view}
                    tooltip={navigationLabel(item, t)}
                    onClick={() => navigate(item.view)}
                  >
                    <item.icon />
                    <span>{navigationLabel(item, t)}</span>
                    {item.unavailable ? <><CircleAlert className="ml-auto text-destructive" /><span className="sr-only">{t("Plugin unavailable")}</span></> : null}
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroup>
        ))}
      </SidebarContent>
      <SidebarFooter>
        <SidebarMenu>
          <SidebarMenuItem>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <SidebarMenuButton size="lg" className="data-[state=open]:bg-sidebar-accent data-[state=open]:text-sidebar-accent-foreground">
                  <CircleUser className="size-7" />
                  <div className="grid flex-1 text-left text-sm leading-tight">
                    <span className="truncate font-medium">{username}</span>
                    <span className="truncate text-xs text-muted-foreground">{t("Administrator")}</span>
                  </div>
                  <EllipsisVertical className="ml-auto size-4" />
                </SidebarMenuButton>
              </DropdownMenuTrigger>
              <DropdownMenuContent
                className="w-(--radix-dropdown-menu-trigger-width) min-w-56 rounded-lg"
                side={isMobile ? "bottom" : "right"}
                align="end"
                sideOffset={4}
              >
                <DropdownMenuLabel className="font-normal">
                  <div className="grid text-sm">
                    <span className="font-medium">{username}</span>
                    <span className="text-xs text-muted-foreground">{t("Administrator")}</span>
                  </div>
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => navigate("security")}>
                  <Shield />{t("Security")}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem disabled={busy} onClick={onLogout}>
                  <LogOut />{t("Sign out")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarFooter>
    </Sidebar>
  )
}

function AdminHeader({ view, onViewChange, version, pluginPages }: { view: AdminView; onViewChange: (view: AdminView) => void; version: string; pluginPages: PluginNavigationPage[] }) {
  const { t } = useI18n()
  const [searchOpen, setSearchOpen] = useState(false)

  useEffect(() => {
    const openSearch = (event: KeyboardEvent) => {
      if (event.key.toLocaleLowerCase() === "k" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault()
        setSearchOpen((open) => !open)
      }
    }
    document.addEventListener("keydown", openSearch)
    return () => document.removeEventListener("keydown", openSearch)
  }, [])

  return (
    <>
      <header className="flex h-(--header-height) shrink-0 items-center gap-2 border-b transition-[width,height] ease-linear group-has-data-[collapsible=icon]/sidebar-wrapper:h-(--header-height)">
        <div className="flex w-full items-center gap-1 px-4 py-3 lg:gap-2 lg:px-6">
          <SidebarTrigger className="-ml-1" aria-label={t("Toggle sidebar")} />
          <Separator orientation="vertical" className="mx-2 data-[orientation=vertical]:h-4" />
          <div className="min-w-0 flex-1 sm:max-w-sm">
            <SearchTrigger onClick={() => setSearchOpen(true)} />
          </div>
          <div className="ml-auto flex items-center gap-2">
            <span className="hidden text-xs text-muted-foreground sm:inline">{version}</span>
            <LanguageSwitcher className="min-w-0" />
            <ThemeToggle />
          </div>
        </div>
      </header>
      <PageSearch open={searchOpen} onOpenChange={setSearchOpen} currentView={view} onViewChange={onViewChange} pluginPages={pluginPages} />
    </>
  )
}

function SearchTrigger({ onClick }: { onClick: () => void }) {
  const { t } = useI18n()
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={t("Search pages")}
      className="relative inline-flex size-8 cursor-pointer items-center justify-center rounded-md border border-input bg-background text-sm font-medium whitespace-nowrap text-muted-foreground shadow-sm transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:ring-1 focus-visible:ring-ring focus-visible:outline-none sm:h-8 sm:w-full sm:justify-start sm:gap-2 sm:px-3 sm:py-1 sm:pr-12 md:w-36 lg:w-56"
    >
      <Search className="size-3.5 sm:mr-2" />
      <span className="hidden sm:inline">{t("Search...")}</span>
      <kbd className="pointer-events-none absolute top-1.5 right-1.5 hidden h-4 select-none items-center gap-1 rounded border bg-muted px-1.5 font-mono text-xs font-medium sm:flex">
        Ctrl K
      </kbd>
    </button>
  )
}

function PageSearch({ open, onOpenChange, currentView, onViewChange, pluginPages }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentView: AdminView
  onViewChange: (view: AdminView) => void
  pluginPages: PluginNavigationPage[]
}) {
  const { t } = useI18n()
  const [query, setQuery] = useState("")
  const items = navigationWithPlugins(pluginPages).flatMap((group) => group.items)
  const visibleItems = items.filter((item) => navigationLabel(item, t).toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()))

  function select(view: AdminView) {
    onViewChange(view)
    onOpenChange(false)
    setQuery("")
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="overflow-hidden p-0 sm:max-w-[640px]">
        <DialogHeader className="sr-only">
          <DialogTitle>{t("Search pages")}</DialogTitle>
          <DialogDescription>{t("Open an administration page")}</DialogDescription>
        </DialogHeader>
        <Input
          className="h-12 rounded-none border-0 border-b px-4 shadow-none focus-visible:ring-0"
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          placeholder={t("Search pages...")}
          autoFocus
        />
        <div className="max-h-[400px] overflow-y-auto px-2 pb-2">
          {visibleItems.length === 0 ? <p className="py-8 text-center text-sm text-muted-foreground">{t("No matching pages")}</p> : null}
          {visibleItems.map((item) => (
            <button
              key={item.view}
              type="button"
              onClick={() => select(item.view)}
              className="flex h-12 w-full cursor-pointer items-center gap-3 rounded-md px-3 text-left text-sm outline-none hover:bg-accent focus-visible:bg-accent"
            >
              <item.icon className="size-4 text-muted-foreground" />
              <span>{navigationLabel(item, t)}</span>
              {item.unavailable ? <><CircleAlert className="ml-auto size-4 text-destructive" /><span className="sr-only">{t("Plugin unavailable")}</span></> : null}
              {currentView === item.view ? <span className={item.unavailable ? "text-xs text-muted-foreground" : "ml-auto text-xs text-muted-foreground"}>{t("Current")}</span> : null}
            </button>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}

function ThemeToggle() {
  const { t } = useI18n()
  const [dark, setDark] = useState(() => document.documentElement.classList.contains("dark"))

  function toggle() {
    const next = !dark
    document.documentElement.classList.toggle("dark", next)
    window.localStorage.setItem("relayward.theme", next ? "dark" : "light")
    setDark(next)
  }

  return (
    <Button variant="outline" size="icon" onClick={toggle} aria-label={t(dark ? "Switch to light mode" : "Switch to dark mode")}>
      {dark ? <Sun /> : <Moon />}
    </Button>
  )
}
