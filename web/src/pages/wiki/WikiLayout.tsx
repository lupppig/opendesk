// The wiki's frame: the page tree on the left, the page or the wiki's home beside it.
// On narrow screens the tree is a sheet opened from the page's header (PagesButton).

import { Outlet } from "@tanstack/react-router";
import { ListTree } from "lucide-react";
import { createContext, useContext, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { Drawer } from "@/components/ui/dialog";
import { WikiTree } from "@/components/wiki/WikiTree";

const SheetCtx = createContext<() => void>(() => {});

/** Opens the page tree on narrow screens; hidden where the tree is beside the page. */
export function PagesButton() {
  const { t } = useTranslation("wiki");
  const open = useContext(SheetCtx);
  return (
    <Button square variant="ghost" size="sm" className="md:hidden" aria-label={t("tree.open")} onClick={open}>
      <ListTree className="size-4" />
    </Button>
  );
}

export default function WikiLayout() {
  const { t } = useTranslation("wiki");
  const [sheet, setSheet] = useState(false);

  return (
    <SheetCtx.Provider value={() => setSheet(true)}>
      <div className="flex h-full min-h-0">
        <aside aria-label={t("tree.label")} className="hidden w-[264px] shrink-0 border-r border-line md:block">
          <WikiTree />
        </aside>
        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          <Outlet />
        </div>
      </div>
      <Drawer open={sheet} onOpenChange={setSheet} title={t("title")} width={340}>
        <WikiTree onNavigate={() => setSheet(false)} />
      </Drawer>
    </SheetCtx.Provider>
  );
}
