import { useQuery } from "@tanstack/react-query";
import { ArrowDownWideNarrow, ArrowUpNarrowWide, Columns2, Filter, Lock, Mail, Rows3, Search, Users, X } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Menu, MenuCheckItem, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuSub, MenuTrigger } from "@/components/ui/menu";
import { Picker } from "@/components/ui/picker";
import { Tooltip } from "@/components/ui/tooltip";
import type { Channel, Priority, Severity } from "@/lib/api/types";
import { encodeFieldFilters, parseFieldFilters, type InboxSearch } from "@/lib/folders";
import { fieldText, useTicketFields, type CustomField } from "./fields";
import { q } from "@/lib/queries";
import { cn } from "@/lib/cn";
import { AnimatedNumber } from "@/components/motion";
import { TagSwatch } from "./TagSwatch";
import { ChannelIcon, PriorityIcon, SeverityChip, channelLabel, priorityLabel, priorityOrder, severityLabel, severityOrder } from "./bits";
import { addressLabel, BrandChip, sortTeams, TeamDot, useTeamsCtx } from "./teams";

/** Address picker values for an alias: the address a ticket was written to. */
const TO = "to:";

export type InboxLayout = "split" | "list";

const sorts = ["last_message_at", "created_at", "priority", "severity", "due"] as const;
const noSeverityMark = <span className="h-[14px] w-[20px] rounded-[4px] border border-dashed border-line-strong" />;

function Chip({ children, onRemove, label }: { children: ReactNode; onRemove: () => void; label: string }) {
  return (
    <motion.span
      layout
      initial={{ opacity: 0, scale: 0.96 }}
      animate={{ opacity: 1, scale: 1 }}
      exit={{ opacity: 0, scale: 0.96 }}
      transition={{ duration: 0.14 }}
      className="inline-flex h-6 shrink-0 items-center gap-1.5 rounded-sm bg-hover pl-2 pr-0.5 text-xs text-fg-2 [&_svg]:size-3"
    >
      {children}
      <button
        type="button"
        aria-label={label}
        onClick={onRemove}
        className="flex size-5 items-center justify-center rounded-xs text-fg-4 outline-none hover:bg-active hover:text-fg focus-visible:shadow-[0_0_0_2px_var(--focus)]"
      >
        <X />
      </button>
    </motion.span>
  );
}

export function ListToolbar({
  title,
  icon,
  count,
  search,
  onSearch,
  searchRef,
  compact,
  layout,
  onLayout,
  below,
  inTeam,
}: {
  /** A row under the title (a team inbox's tabs). */
  below?: ReactNode;
  /** A team inbox: the team filter is the page itself. */
  inTeam?: boolean;
  title: string;
  icon: ReactNode;
  count?: number | null;
  search: InboxSearch;
  onSearch: (s: InboxSearch) => void;
  searchRef: React.RefObject<HTMLInputElement | null>;
  compact: boolean;
  /** The list/split choice; offered on wide screens only. */
  layout?: InboxLayout;
  onLayout?: (l: InboxLayout) => void;
}) {
  const { t } = useTranslation("inbox");
  const reduced = useReducedMotion();
  const { data: tags } = useQuery(q.tags());
  const teams = useTeamsCtx();
  const mailboxes = teams.mailboxes;
  const [addressOpen, setAddressOpen] = useState(false);
  const openingAddress = useRef(false);
  const [term, setTerm] = useState(search.q ?? "");
  const [searchOpen, setSearchOpen] = useState(!!search.q);
  const first = useRef(true);

  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    // Only a real change: a spurious run (React's double mount in development) must not
    // navigate, or it can pull the page back while the person is leaving it.
    if ((term.trim() || undefined) === (search.q || undefined)) return;
    const tm = setTimeout(() => onSearch({ ...search, q: term.trim() || undefined }), 250);
    return () => clearTimeout(tm);
    // Only the typed term drives this.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [term]);

  useEffect(() => {
    if (searchOpen) searchRef.current?.focus();
  }, [searchOpen, searchRef]);

  const priorities = (search.priority?.split(",") ?? []) as Priority[];
  const sort = search.sort ?? "last_message_at";
  const order = search.order ?? (sort === "due" ? "asc" : "desc");
  const severities = (search.severity?.split(",") ?? []) as (Severity | "none")[];
  const toggleSeverity = (v: Severity | "none") => {
    const next = severities.includes(v) ? severities.filter((x) => x !== v) : [...severities, v];
    onSearch({ ...search, severity: next.length ? next.join(",") : undefined });
  };
  const togglePriority = (p: Priority) => {
    const next = priorities.includes(p) ? priorities.filter((x) => x !== p) : [...priorities, p];
    onSearch({ ...search, priority: next.length ? next.join(",") : undefined });
  };
  const { data: allFields } = useTicketFields();
  const { data: members } = useQuery(q.members());
  const pickable = (allFields ?? []).filter((f) => f.type === "select" || f.type === "multi_select" || f.type === "user" || f.type === "checkbox");
  const sortable = (allFields ?? []).filter((f) => ["text", "number", "date", "checkbox", "select"].includes(f.type));
  const fieldFilters = parseFieldFilters(search.fields);
  const setFieldFilters = (next: typeof fieldFilters) => onSearch({ ...search, fields: encodeFieldFilters(next) });
  const fieldValues = (f: CustomField) => fieldFilters.find((x) => x.key === f.key)?.value.split(",").filter(Boolean) ?? [];
  const toggleField = (f: CustomField, v: string) => {
    const rest = fieldFilters.filter((x) => x.key !== f.key);
    if (f.type === "checkbox") return setFieldFilters(fieldValues(f).includes(v) ? rest : [...rest, { key: f.key, op: "eq", value: v }]);
    const cur = fieldValues(f);
    const vals = cur.includes(v) ? cur.filter((x) => x !== v) : [...cur, v];
    setFieldFilters(vals.length ? [...rest, { key: f.key, op: "any", value: vals.join(",") }] : rest);
  };
  const memberName = (id: string) => (id === "me" ? t("fields.me") : members?.find((m) => m.user.id === id)?.user.name);
  // Team: any of ids, "mine" and "none".
  const teamSel = inTeam ? [] : (search.team?.split(",").filter(Boolean) ?? []);
  const toggleTeam = (v: string) => {
    const next = teamSel.includes(v) ? teamSel.filter((x) => x !== v) : [...teamSel, v];
    onSearch({ ...search, team: next.length ? next.join(",") : undefined });
  };
  const teamLabel = (v: string) => (v === "mine" ? t("teams.myTeams") : v === "none" ? t("props.noTeam") : (teams.team(v)?.name ?? t("teams.team")));
  const showTeams = teams.hasTeams && !inTeam;
  const brandSel = teams.multiBrand ? teams.brand(search.brand) : undefined;
  // Each mailbox, then its aliases: an alias filters on the address the ticket was written to.
  const addressItems = mailboxes.flatMap((m) => {
    const group = teams.multiBrand ? (teams.brand(m.brand_id)?.name ?? "") : undefined;
    return [
      {
        value: m.id,
        label: addressLabel(m),
        keywords: [m.email, ...(m.aliases ?? [])],
        group,
        icon: <TeamDot team={teams.team(m.team_id)} />,
        hint: m.private ? <Lock className="size-3" /> : undefined,
      },
      ...(m.aliases ?? []).map((a) => ({
        value: TO + a,
        label: a,
        keywords: [m.email, m.name],
        group,
        icon: <TeamDot team={teams.team(m.alias_teams?.[a] ?? m.team_id)} />,
        hint: <span className="text-[11px] text-fg-4">{t("filter.alias")}</span>,
      })),
    ];
  });
  const activeMailbox = mailboxes.find((m) => m.id === search.mailbox);
  const activeAddress = activeMailbox?.email ?? search.to;
  const activeFilters =
    priorities.length +
    severities.length +
    (search.channel ? 1 : 0) +
    (search.tag ? 1 : 0) +
    (search.mailbox ? 1 : 0) +
    (search.to ? 1 : 0) +
    teamSel.length +
    (brandSel ? 1 : 0) +
    fieldFilters.length;
  const closeSearch = () => {
    setTerm("");
    setSearchOpen(false);
    onSearch({ ...search, q: undefined });
  };
  const activeTag = tags?.find((x) => x.name === search.tag);

  return (
    <div className="shrink-0 border-b border-line">
      <div className="flex h-12 items-center gap-1.5 pl-4 pr-2.5">
        <span className="flex text-fg-3 [&_svg]:size-4">{icon}</span>
        {searchOpen ? (
          <motion.div
            initial={reduced ? false : { opacity: 0, x: -4 }}
            animate={{ opacity: 1, x: 0 }}
            transition={{ duration: 0.16 }}
            className="ml-1 flex min-w-0 flex-1 items-center gap-1.5"
          >
            <input
              ref={searchRef}
              value={term}
              onChange={(e) => setTerm(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  e.stopPropagation();
                  closeSearch();
                }
                if (e.key === "ArrowDown" || e.key === "Enter") (e.target as HTMLInputElement).blur();
              }}
              placeholder={t("toolbar.searchIn", { title: title.toLowerCase() })}
              aria-label={t("toolbar.searchTickets")}
              className="h-7 min-w-0 flex-1 bg-transparent text-[13px] outline-none placeholder:text-fg-4"
            />
            <Button variant="ghost" size="sm" square aria-label={t("toolbar.closeSearch")} icon={<X className="size-3.5" />} onClick={closeSearch} />
          </motion.div>
        ) : (
          <div className="ml-1 flex min-w-0 flex-1 items-baseline gap-2">
            <h1 className="truncate text-[14px] font-semibold tracking-[-0.01em]">{title}</h1>
            {count !== null && count !== undefined && (
              <AnimatedNumber value={count} flash className="mono text-[11.5px] text-fg-4" />
            )}
          </div>
        )}
        {!searchOpen && (
          <Tooltip content={t("toolbar.search")} shortcut="/">
            <Button variant="ghost" size="sm" square aria-label="Search" icon={<Search className="size-3.5" />} onClick={() => setSearchOpen(true)} />
          </Tooltip>
        )}
        <Menu>
          <Tooltip content={t("toolbar.filter")}>
            <MenuTrigger asChild>
              <Button
                variant="ghost"
                size="sm"
                square={compact || !activeFilters}
                aria-label="Filter"
                icon={<Filter className="size-3.5" />}
                className={cn(activeFilters > 0 && "bg-hover text-fg")}
              >
                {!compact && activeFilters > 0 ? <span className="mono text-[11px] tabular-nums">{activeFilters}</span> : null}
              </Button>
            </MenuTrigger>
          </Tooltip>
          <MenuContent
            align="end"
            className="w-[210px]"
            onCloseAutoFocus={(e) => {
              // The address list opens next and takes the focus.
              if (openingAddress.current) e.preventDefault();
              openingAddress.current = false;
            }}
          >
            <MenuSub label={t("filter.priority")}>
              {priorityOrder.map((p) => (
                <MenuCheckItem key={p} checked={priorities.includes(p)} onSelect={() => togglePriority(p)} icon={<PriorityIcon priority={p} />}>
                  {priorityLabel[p]}
                </MenuCheckItem>
              ))}
            </MenuSub>
            <MenuSub label={t("filter.severity")}>
              {severityOrder.map((v) => (
                <MenuCheckItem key={v} checked={severities.includes(v)} onSelect={() => toggleSeverity(v)} icon={<SeverityChip severity={v} />}>
                  {severityLabel[v]}
                </MenuCheckItem>
              ))}
              <MenuCheckItem checked={severities.includes("none")} onSelect={() => toggleSeverity("none")} icon={noSeverityMark}>
                {t("filter.noSeverity")}
              </MenuCheckItem>
            </MenuSub>
            <MenuSub label={t("filter.channel")}>
              {(["email", "chat", "api", "portal"] as Channel[]).map((c) => (
                <MenuCheckItem
                  key={c}
                  checked={search.channel === c}
                  onSelect={() => onSearch({ ...search, channel: search.channel === c ? undefined : c })}
                  icon={<ChannelIcon channel={c} />}
                >
                  {channelLabel[c]}
                </MenuCheckItem>
              ))}
            </MenuSub>
            <MenuSub label={t("filter.tag")}>
              <div className="max-h-[260px] overflow-y-auto">
                {(tags ?? []).map((x) => (
                  <MenuCheckItem
                    key={x.id}
                    checked={search.tag === x.name}
                    onSelect={() => onSearch({ ...search, tag: search.tag === x.name ? undefined : x.name })}
                    icon={<TagSwatch name={x.name} color={x.color} />}
                  >
                    {x.name}
                  </MenuCheckItem>
                ))}
                {!tags?.length && <p className="px-2 py-1.5 text-xs text-fg-4">{t("filter.noTags")}</p>}
              </div>
            </MenuSub>
            {showTeams && (
              <MenuSub label={t("filter.team")}>
                <div className="max-h-[300px] overflow-y-auto">
                  {teams.myTeams.size > 0 && (
                    <MenuCheckItem checked={teamSel.includes("mine")} onSelect={() => toggleTeam("mine")} icon={<Users />}>
                      {t("teams.myTeams")}
                    </MenuCheckItem>
                  )}
                  {sortTeams(teams.teams.filter(teams.canSeeTeam), teams.myTeams).map((x) => (
                    <MenuCheckItem key={x.id} checked={teamSel.includes(x.id)} onSelect={() => toggleTeam(x.id)} icon={<TeamDot team={x} />}>
                      <span className="flex items-center gap-1.5">
                        {x.name}
                        {x.private && <Lock className="!size-3 !text-fg-4" />}
                      </span>
                    </MenuCheckItem>
                  ))}
                  <MenuCheckItem
                    checked={teamSel.includes("none")}
                    onSelect={() => toggleTeam("none")}
                    icon={<span className="size-2 rounded-full border border-dashed border-line-strong" />}
                  >
                    {t("props.noTeam")}
                  </MenuCheckItem>
                </div>
              </MenuSub>
            )}
            {teams.multiBrand && (
              <MenuSub label={t("filter.brand")}>
                {teams.brands.map((b) => (
                  <MenuCheckItem
                    key={b.id}
                    checked={search.brand === b.id}
                    onSelect={() => onSearch({ ...search, brand: search.brand === b.id ? undefined : b.id })}
                    icon={<span className="size-2 rounded-[2px] bg-fg-4" style={b.color ? { backgroundColor: b.color } : undefined} />}
                  >
                    {b.name}
                  </MenuCheckItem>
                ))}
              </MenuSub>
            )}
            {mailboxes.length > 0 && (
              <MenuItem
                icon={<Mail />}
                onSelect={() => {
                  openingAddress.current = true;
                  setTimeout(() => setAddressOpen(true), 0);
                }}
              >
                <span className="flex items-center justify-between gap-2">
                  {t("filter.address")}
                  {activeAddress && <span className="mono truncate text-[11px] text-fg-4">{activeAddress}</span>}
                </span>
              </MenuItem>
            )}
            {pickable.length > 0 && (
              <MenuSub label={t("fields.filter")}>
                {pickable.map((f) => (
                  <MenuSub key={f.id} label={f.name}>
                    <div className="max-h-[260px] overflow-y-auto">
                      {f.type === "checkbox" ? (
                        <>
                          <MenuCheckItem checked={fieldValues(f).includes("true")} onSelect={() => toggleField(f, "true")}>
                            {t("fields.yes")}
                          </MenuCheckItem>
                          <MenuCheckItem checked={fieldValues(f).includes("false")} onSelect={() => toggleField(f, "false")}>
                            {t("fields.no")}
                          </MenuCheckItem>
                        </>
                      ) : f.type === "user" ? (
                        [{ id: "me", name: t("fields.me") }, ...(members ?? []).map((m) => ({ id: m.user.id, name: m.user.name }))].map((m) => (
                          <MenuCheckItem key={m.id} checked={fieldValues(f).includes(m.id)} onSelect={() => toggleField(f, m.id)}>
                            {m.name}
                          </MenuCheckItem>
                        ))
                      ) : (
                        f.options.map((o) => (
                          <MenuCheckItem key={o.id} checked={fieldValues(f).includes(o.id)} onSelect={() => toggleField(f, o.id)} icon={<TagSwatch name={o.label} color={o.color} />}>
                            {o.label}
                          </MenuCheckItem>
                        ))
                      )}
                    </div>
                  </MenuSub>
                ))}
              </MenuSub>
            )}
            {activeFilters > 0 && (
              <>
                <MenuSeparator />
                <MenuCheckItem checked={false} onSelect={() => onSearch({ sort: search.sort, order: search.order, q: search.q, tab: search.tab })}>
                  {t("filter.clear")}
                </MenuCheckItem>
              </>
            )}
          </MenuContent>
        </Menu>
        <Picker
          open={addressOpen}
          onOpenChange={setAddressOpen}
          align="end"
          width={320}
          placeholder={t("filter.searchAddresses")}
          emptyText={t("filter.noAddresses")}
          value={search.mailbox ?? (search.to ? TO + search.to : null)}
          items={addressItems}
          onSelect={(v) => {
            const to = v.startsWith(TO) ? v.slice(TO.length) : undefined;
            const mailbox = to ? undefined : v;
            onSearch({ ...search, mailbox: mailbox && search.mailbox !== mailbox ? mailbox : undefined, to: to && search.to !== to ? to : undefined });
          }}
          trigger={<span aria-hidden className="pointer-events-none -ml-1.5 h-7 w-0" />}
        />
        <Menu>
          <Tooltip content={t("toolbar.sort")}>
            <MenuTrigger asChild>
              <Button
                variant="ghost"
                size="sm"
                square
                aria-label="Sort"
                icon={order === "asc" ? <ArrowUpNarrowWide className="size-3.5" /> : <ArrowDownWideNarrow className="size-3.5" />}
                className={cn(sort !== "last_message_at" && "bg-hover text-fg")}
              />
            </MenuTrigger>
          </Tooltip>
          <MenuContent align="end" className="w-[190px]">
            <MenuLabel>{t("sort.by")}</MenuLabel>
            {sorts.map((s) => (
              <MenuCheckItem key={s} checked={sort === s} onSelect={() => onSearch({ ...search, sort: s === "last_message_at" ? undefined : s, order: undefined })}>
                {t(`sort.${s}`)}
              </MenuCheckItem>
            ))}
            {sortable.length > 0 && (
              <>
                <MenuSeparator />
                <MenuLabel>{t("fields.filter")}</MenuLabel>
                {sortable.map((f) => (
                  <MenuCheckItem key={f.id} checked={sort === `field.${f.key}`} onSelect={() => onSearch({ ...search, sort: `field.${f.key}`, order: undefined })}>
                    {f.name}
                  </MenuCheckItem>
                ))}
              </>
            )}
            <MenuSeparator />
            <MenuCheckItem checked={order === "desc"} onSelect={() => onSearch({ ...search, order: "desc" })}>
              {sort === "due" ? t("sort.latest") : t("sort.newest")}
            </MenuCheckItem>
            <MenuCheckItem checked={order === "asc"} onSelect={() => onSearch({ ...search, order: "asc" })}>
              {sort === "due" ? t("sort.soonest") : t("sort.oldest")}
            </MenuCheckItem>
          </MenuContent>
        </Menu>
        {layout && onLayout && (
          <>
            <span className="mx-0.5 h-4 w-px bg-line" />
            <Tooltip content={layout === "split" ? t("toolbar.listView") : t("toolbar.splitView")}>
              <Button
                variant="ghost"
                size="sm"
                square
                aria-label={t("toolbar.splitView")}
                aria-pressed={layout === "split"}
                icon={layout === "split" ? <Columns2 className="size-3.5" /> : <Rows3 className="size-3.5" />}
                onClick={() => onLayout(layout === "split" ? "list" : "split")}
              />
            </Tooltip>
          </>
        )}
      </div>
      {below}
      <AnimatePresence initial={false}>
        {activeFilters > 0 && (
          <motion.div
            initial={reduced ? { opacity: 0 } : { height: 0, opacity: 0 }}
            animate={reduced ? { opacity: 1 } : { height: "auto", opacity: 1 }}
            exit={reduced ? { opacity: 0 } : { height: 0, opacity: 0 }}
            transition={{ duration: 0.18, ease: [0.16, 1, 0.3, 1] }}
            className="overflow-hidden"
          >
            <div className="flex items-center gap-1.5 overflow-x-auto px-3.5 pb-2.5">
              <AnimatePresence initial={false}>
                {priorities.map((p) => (
                  <Chip key={p} label={t("filter.remove")} onRemove={() => togglePriority(p)}>
                    <PriorityIcon priority={p} />
                    {priorityLabel[p]}
                  </Chip>
                ))}
                {severities.map((v) => (
                  <Chip key={`sev-${v}`} label={t("filter.remove")} onRemove={() => toggleSeverity(v)}>
                    {v === "none" ? noSeverityMark : <SeverityChip severity={v} />}
                    {v === "none" ? t("filter.noSeverity") : severityLabel[v]}
                  </Chip>
                ))}
                {search.channel && (
                  <Chip key="channel" label={t("filter.remove")} onRemove={() => onSearch({ ...search, channel: undefined })}>
                    <ChannelIcon channel={search.channel as Channel} />
                    {channelLabel[search.channel as Channel] ?? search.channel}
                  </Chip>
                )}
                {search.tag && (
                  <Chip key="tag" label={t("filter.remove")} onRemove={() => onSearch({ ...search, tag: undefined })}>
                    <TagSwatch name={search.tag} color={activeTag?.color} />
                    {search.tag}
                  </Chip>
                )}
                {fieldFilters.map((ff) => {
                  const f = allFields?.find((x) => x.key === ff.key);
                  return (
                    <Chip key={`f-${ff.key}`} label={t("filter.remove")} onRemove={() => setFieldFilters(fieldFilters.filter((x) => x.key !== ff.key))}>
                      <span className="text-fg-3">{f?.name ?? ff.key}</span>
                      {ff.value.split(",").map((v) => fieldText(f, f?.type === "checkbox" ? v === "true" : v, memberName)).join(", ")}
                    </Chip>
                  );
                })}
                {search.mailbox && (
                  <Chip key="mailbox" label={t("filter.remove")} onRemove={() => onSearch({ ...search, mailbox: undefined })}>
                    <Mail />
                    <span className="mono text-[11.5px]">{activeMailbox?.email ?? t("filter.address")}</span>
                  </Chip>
                )}
                {search.to && (
                  <Chip key="to" label={t("filter.remove")} onRemove={() => onSearch({ ...search, to: undefined })}>
                    <Mail />
                    <span className="text-fg-3">{t("filter.writtenTo")}</span>
                    <span className="mono text-[11.5px]">{search.to}</span>
                  </Chip>
                )}
                {teamSel.map((v) => (
                  <Chip key={`team-${v}`} label={t("filter.remove")} onRemove={() => toggleTeam(v)}>
                    {v === "mine" ? <Users /> : v === "none" ? <span className="size-2 rounded-full border border-dashed border-line-strong" /> : <TeamDot team={teams.team(v)} />}
                    {teamLabel(v)}
                  </Chip>
                ))}
                {brandSel && (
                  <Chip key="brand" label={t("filter.remove")} onRemove={() => onSearch({ ...search, brand: undefined })}>
                    <BrandChip brand={brandSel} className="-ml-1.5 h-5 bg-transparent shadow-none" />
                  </Chip>
                )}
              </AnimatePresence>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}
