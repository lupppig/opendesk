// The person's clock: 12 or 24 hour times in the app and in their notification
// emails. Saved to the account (PATCH /v1/me) as soon as it changes, so it
// follows them to every browser.

import { useMutation } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Row } from "@/components/layout";
import { Select } from "@/components/ui/select";
import { toast } from "@/components/ui/toast";
import { api } from "@/lib/api/endpoints";
import type { TimeFormat, User } from "@/lib/api/types";
import { updateUser, useUser } from "@/lib/session";

export function ClockSetting() {
  const { t } = useTranslation("settings");
  const user = useUser();
  const save = useMutation({
    mutationFn: (time_format: TimeFormat) => api.me.update({ time_format }),
    onMutate: (time_format) => {
      const prev: User = user;
      updateUser({ ...user, time_format });
      return { prev };
    },
    onError: (e, _v, ctx) => {
      if (ctx) updateUser(ctx.prev);
      toast.error(e);
    },
    onSuccess: (u) => {
      updateUser(u);
      toast.success(t("profile.clock.saved"));
    },
  });
  return (
    <Row label={t("profile.clock.label")} description={t("profile.clock.labelHint")} htmlFor="time-format">
      <Select<TimeFormat>
        id="time-format"
        aria-label={t("profile.clock.label")}
        value={user.time_format ?? "auto"}
        onChange={(v) => v !== user.time_format && save.mutate(v)}
        disabled={save.isPending}
        className="w-48"
        options={[
          { value: "auto", label: t("profile.clock.auto") },
          { value: "12h", label: t("profile.clock.h12") },
          { value: "24h", label: t("profile.clock.h24") },
        ]}
      />
    </Row>
  );
}
