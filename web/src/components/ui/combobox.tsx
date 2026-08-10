import * as React from "react"
import { CheckIcon, ChevronsUpDownIcon } from "lucide-react"

import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"

export interface ComboboxOption {
  value: string
  label: string
  keywords?: string[]
}

interface ComboboxProps {
  value: string
  onValueChange: (value: string) => void
  options: ComboboxOption[]
  searchPlaceholder: string
  emptyText: string
  placeholder?: string
  customValueLabel?: (value: string) => string
  allowCustomValue?: boolean
  disabled?: boolean
  required?: boolean
  id?: string
  className?: string
  "aria-label"?: string
  "aria-labelledby"?: string
  leadingIcon?: React.ReactNode
  align?: "start" | "center" | "end"
}

function Combobox({
  value,
  onValueChange,
  options,
  searchPlaceholder,
  emptyText,
  placeholder,
  customValueLabel = (candidate) => candidate,
  allowCustomValue = false,
  disabled = false,
  required = false,
  id,
  className,
  leadingIcon,
  align = "start",
  ...ariaProps
}: ComboboxProps) {
  const [open, setOpen] = React.useState(false)
  const [search, setSearch] = React.useState("")
  const triggerRef = React.useRef<HTMLButtonElement>(null)
  const searchRef = React.useRef<HTMLInputElement>(null)
  const suppressFocusOpen = React.useRef(false)
  const returnFocusOnClose = React.useRef(false)
  const selected = options.find((option) => option.value === value)
  const normalizedSearch = search.trim()
  const exactOption = options.some((option) => (
    option.value.toLocaleLowerCase() === normalizedSearch.toLocaleLowerCase() ||
    option.label.toLocaleLowerCase() === normalizedSearch.toLocaleLowerCase()
  ))

  function changeOpen(next: boolean) {
    if (!next) {
      setSearch("")
    }
    setOpen(next)
  }

  function select(next: string) {
    returnFocusOnClose.current = true
    onValueChange(next)
    changeOpen(false)
  }

  return (
    <Popover open={open} onOpenChange={changeOpen}>
      <PopoverTrigger asChild>
        <Button
          ref={triggerRef}
          id={id}
          className={cn("w-full justify-between font-normal", className)}
          variant="outline"
          role="combobox"
          aria-expanded={open}
          aria-required={required || undefined}
          disabled={disabled}
          onFocus={() => {
            if (suppressFocusOpen.current) {
              suppressFocusOpen.current = false
              return
            }
            setOpen(true)
          }}
          onClick={(event) => {
            event.preventDefault()
            setOpen(true)
          }}
          type="button"
          {...ariaProps}
        >
          <span className="flex min-w-0 items-center gap-2">
            {leadingIcon}
            <span className={cn("truncate", !selected && value === "" && "text-muted-foreground")}>
              {selected?.label ?? (value || placeholder)}
            </span>
          </span>
          <ChevronsUpDownIcon className="size-4 shrink-0 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent
        align={align}
        className="w-[var(--radix-popover-trigger-width)] min-w-56 p-0"
        onOpenAutoFocus={(event) => {
          event.preventDefault()
          window.requestAnimationFrame(() => searchRef.current?.focus())
        }}
        onEscapeKeyDown={() => { returnFocusOnClose.current = true }}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          if (!returnFocusOnClose.current) return
          returnFocusOnClose.current = false
          suppressFocusOpen.current = true
          triggerRef.current?.focus({ preventScroll: true })
        }}
      >
        <Command>
          <CommandInput ref={searchRef} value={search} onValueChange={setSearch} placeholder={searchPlaceholder} />
          <CommandList>
            <CommandEmpty>{emptyText}</CommandEmpty>
            <CommandGroup>
              {allowCustomValue && normalizedSearch !== "" && !exactOption ? (
                <CommandItem forceMount value={normalizedSearch} onSelect={() => select(normalizedSearch)}>
                  <CheckIcon className="invisible" />
                  <span className="truncate">{customValueLabel(normalizedSearch)}</span>
                </CommandItem>
              ) : null}
              {options.map((option) => (
                <CommandItem
                  key={option.value}
                  value={[option.label, option.value, ...(option.keywords ?? [])].join(" ")}
                  onSelect={() => select(option.value)}
                >
                  <CheckIcon className={cn("size-4", value === option.value ? "opacity-100" : "opacity-0")} />
                  <span className="truncate">{option.label}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

export { Combobox }
