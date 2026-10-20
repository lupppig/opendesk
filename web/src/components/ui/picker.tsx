import { Command } from "cmdk";
import { Popover as P } from "radix-ui";
import { Check, Plus } from "lucide-react";
import { useState, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { floatingPanel } from "./popover";

export type PickerItem = {
  value: string;
  label: string;
  icon?: ReactNode;
  hint?: ReactNode;
  keywords?: string[];
  group?: string;
};

/**
 * The search row of a popover picker. The panel clips its overflow, so the
 * field sits inside a little padding and draws its focus ring inset: the ring
 * stays whole on every side.
 */
export const pickerSearch = {
  row: "border-b border-line p-1",
  input:
    "h-7 w-full rounded-md bg-transparent px-1.5 text-[12.5px] text-fg outline-none placeholder:text-fg-4 focus-visible:shadow-[inset_0_0_0_1.5px_var(--focus)]",
};

type Common = {
  trigger: ReactNode;
  items: PickerItem[];
  placeholder?: string;
  emptyText?: string;
  open?: boolean;
  onOpenChange?: (o: boolean) => void;
  align?: "start" | "center" | "end";
  side?: "top" | "bottom" | "left" | "right";
  width?: number;
  /** Offer "Create <query>" when nothing matches exactly. */
  onCreate?: (q: string) => void;
  createLabel?: string;
  footer?: ReactNode;
  /** Server-side search: called as the query changes, and local filtering is off. */
  onQueryChange?: (q: string) => void;
};

type Single = Common & { multi?: false; value: string | null; onSelect: (v: string) => void };
type Multi = Common & { multi: true; values: string[]; onChange: (v: string[]) => void };

/**
 * The one picker: a popover with a search field and keyboard-navigable rows.
 * Assignee, team, status, priority, tags, mailboxes, saved replies all use it.
 */
export function Picker(props: Single | Multi) {
  const { trigger, items, placeholder = "Search", emptyText = "No matches", align = "start", side = "bottom", width = 240 } = props;
  const [innerOpen, setInnerOpen] = useState(false);
  const open = props.open ?? innerOpen;
  const setOpen = (o: boolean) => {
    props.onOpenChange?.(o);
    if (props.open === undefined) setInnerOpen(o);
    if (!o) setQuery("");
  };
  const [query, setQueryState] = useState("");
  const setQuery = (v: string) => {
    setQueryState(v);
    props.onQueryChange?.(v);
  };

  const selected = (v: string) => (props.multi ? props.values.includes(v) : props.value === v);
  const choose = (v: string) => {
    if (props.multi) {
      props.onChange(props.values.includes(v) ? props.values.filter((x) => x !== v) : [...props.values, v]);
    } else {
      props.onSelect(v);
      setOpen(false);
    }
  };

  const groups = Array.from(new Set(items.map((i) => i.group ?? "")));
  const exact = items.some((i) => i.label.toLowerCase() === query.trim().toLowerCase());

  return (
    <P.Root open={open} onOpenChange={setOpen}>
      <P.Trigger asChild>{trigger}</P.Trigger>
      <P.Portal>
        <P.Content
          align={align}
          side={side}
          sideOffset={6}
          collisionPadding={8}
          style={{ width }}
          className={cn(floatingPanel, "overflow-hidden p-0")}
          onClick={(e) => e.stopPropagation()}
        >
          <Command loop shouldFilter={!props.onQueryChange} className="flex flex-col">
            <div className={pickerSearch.row}>
              <Command.Input aria-label={placeholder} autoFocus value={query} onValueChange={setQuery} placeholder={placeholder} className={pickerSearch.input} />
            </div>
            <Command.List className="max-h-[280px] overflow-y-auto p-1">
              <Command.Empty className="px-2 py-5 text-center text-xs text-fg-3">{emptyText}</Command.Empty>
              {groups.map((g) => (
                <Command.Group
                  key={g || "_"}
                  heading={g || undefined}
                  className="[&_[cmdk-group-heading]]:label-caps [&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:pb-1 [&_[cmdk-group-heading]]:pt-2"
                >
                  {items
                    .filter((i) => (i.group ?? "") === g)
                    .map((i) => (
                      <Command.Item
                        key={i.value}
                        value={`${i.label} ${i.value}`}
                        keywords={i.keywords}
                        onSelect={() => choose(i.value)}
                        className="flex h-7 cursor-default items-center gap-2 rounded-md px-2 text-[12.5px] text-fg data-[selected=true]:bg-hover [&_svg]:size-3.5 [&_svg]:shrink-0"
                      >
                        {props.multi && (
                          <span
                            className={cn(
                              "flex size-3.5 items-center justify-center rounded-sm border",
                              selected(i.value) ? "border-inverse bg-inverse text-inverse-fg" : "border-line-strong",
                            )}
                          >
                            {selected(i.value) && <Check className="!size-2.5" strokeWidth={3.5} />}
                          </span>
                        )}
                        {i.icon}
                        <span className="flex-1 truncate">{i.label}</span>
                        {i.hint && <span className="shrink-0 text-[11px] text-fg-4">{i.hint}</span>}
                        {!props.multi && selected(i.value) && <Check className="text-fg" />}
                      </Command.Item>
                    ))}
                </Command.Group>
              ))}
              {props.onCreate && query.trim() && !exact && (
                <Command.Item
                  value={`__create ${query}`}
                  onSelect={() => {
                    props.onCreate!(query.trim());
                    setQuery("");
                  }}
                  className="flex h-7 cursor-default items-center gap-2 rounded-md px-2 text-[12.5px] text-fg-2 data-[selected=true]:bg-hover"
                >
                  <Plus className="size-3.5" />
                  {props.createLabel ?? "Create"} <span className="font-medium text-fg">“{query.trim()}”</span>
                </Command.Item>
              )}
            </Command.List>
            {props.footer && <div className="border-t border-line p-1">{props.footer}</div>}
          </Command>
        </P.Content>
      </P.Portal>
    </P.Root>
  );
}
