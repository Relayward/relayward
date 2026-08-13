import {
  Activity,
  Gauge,
  Network,
  Route,
  ServerCog,
  Shield,
  SlidersHorizontal,
  type LucideIcon,
} from "lucide-react";

import type { PluginNavigationIcon } from "./api";

export const pluginIcons: Record<PluginNavigationIcon, LucideIcon> = {
  activity: Activity,
  gauge: Gauge,
  network: Network,
  route: Route,
  "server-cog": ServerCog,
  shield: Shield,
  "sliders-horizontal": SlidersHorizontal,
};
