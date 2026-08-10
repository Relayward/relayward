import type { ComboboxOption } from "@/components/ui/combobox";

const fallbackTimezones = [
  "UTC",
  "Asia/Shanghai",
  "Asia/Singapore",
  "Europe/London",
  "America/New_York",
];

const supportedValuesOf = (Intl as typeof Intl & {
  supportedValuesOf?: (key: string) => string[];
}).supportedValuesOf;

const supportedTimezones = supportedValuesOf === undefined
  ? fallbackTimezones
  : ["UTC", ...supportedValuesOf("timeZone")];

export function timezoneOptions(current?: string): ComboboxOption[] {
  const values = new Set(supportedTimezones);
  if (current) values.add(current);
  return Array.from(values)
    .sort((left, right) => left.localeCompare(right))
    .map((value) => ({ value, label: value }));
}
