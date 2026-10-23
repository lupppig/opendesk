// A strip at the top of the work panel that asks once for notification permission, so
// new tickets and replies reach people while opendesk is in the background. Until it is
// allowed, they land only under the bell. "Not now" puts it away for two weeks; a choice
// made in the browser (allowed or blocked) puts it away for good.

import { useQuery } from "@tanstack/react-query";
import { BellRing, X } from "lucide-react";
import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { q } from "@/lib/queries";
import { cn } from "@/lib/cn";
import { dur, ease } from "@/lib/motion";
import { tone } from "@/lib/tones";
import { subscribeWebPush } from "@/lib/webPush";

const KEY = "od-notify-ask";
const AGAIN_MS = 14 * 24 * 60 * 60 * 1000;

function permission(): NotificationPermission | null {
  return typeof window !== "undefined" && "Notification" in window ? window.Notification.permission : null;
}

function putAway(): boolean {
  const at = Number(localStorage.getItem(KEY) ?? 0);
  return at > 0 && Date.now() - at < AGAIN_MS;
}

export function AllowNotificationsBanner() {
  const { t } = useTranslation("shell");
  const reduce = useReducedMotion();
  const { data: config } = useQuery(q.authConfig());
  const [perm, setPerm] = useState(permission);
  const [away, setAway] = useState(putAway);
  useEffect(() => {
    const sync = () => setPerm(permission());
    window.addEventListener("focus", sync);
    return () => window.removeEventListener("focus", sync);
  }, []);
  const open = perm === "default" && !away;

  return (
    <AnimatePresence initial={false}>
      {open && (
        <motion.div
          key="allow-notifications"
          initial={reduce ? { opacity: 0 } : { opacity: 0, height: 0 }}
          animate={reduce ? { opacity: 1 } : { opacity: 1, height: "auto" }}
          exit={reduce ? { opacity: 0 } : { opacity: 0, height: 0 }}
          transition={{ duration: dur.slow, ease: ease.out }}
          className="shrink-0 overflow-hidden"
        >
          <div role="status" className={cn("flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b px-4 py-2", tone.blue.soft, tone.blue.line)}>
            <BellRing className={cn("size-3.5 shrink-0", tone.blue.text)} aria-hidden />
            <p className="min-w-0 flex-1 text-[12.5px] leading-[18px]">
              <span className="font-medium">{t("notifications.allow.title")}</span>{" "}
              <span className="text-fg-3">{t("notifications.allow.hint")}</span>
            </p>
            <div className="flex shrink-0 items-center gap-1.5">
              <Button
                size="xs"
                variant="primary"
                onClick={() =>
                  void window.Notification.requestPermission().then((p) => {
                    setPerm(p);
                    if (p === "granted") void subscribeWebPush(config?.web_push_key).catch(() => undefined);
                  })
                }
              >
                {t("notifications.allow.allow")}
              </Button>
              <Button
                size="xs"
                variant="ghost"
                square
                icon={<X />}
                aria-label={t("notifications.allow.later")}
                onClick={() => {
                  localStorage.setItem(KEY, String(Date.now()));
                  setAway(true);
                }}
              />
            </div>
          </div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
