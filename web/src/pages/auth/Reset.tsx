import { getRouteApi, Link } from "@tanstack/react-router";
import { Check } from "lucide-react";
import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { z } from "zod";
import { AuthHeading } from "@/app/AuthLayout";
import { Button } from "@/components/ui/button";
import { AuthField, FieldError, Notice, PasswordInput, StrengthMeter } from "@/components/auth/parts";
import { api } from "@/lib/api/endpoints";
import { errorMessage, isApiError } from "@/lib/api/client";
import { tone } from "@/lib/tones";
import { cn } from "@/lib/cn";

const route = getRouteApi("/auth/reset");

const schema = z
  .object({
    password: z
      .string()
      .refine((s) => [...s].length >= 12, "Use at least 12 characters.")
      .refine((s) => [...s].length <= 128, "Use at most 128 characters."),
    confirm: z.string(),
  })
  .refine((v) => v.password === v.confirm, { message: "The passwords don't match.", path: ["confirm"] });
type Values = z.infer<typeof schema>;

export default function Reset() {
  const { token } = route.useSearch();
  const {
    register,
    handleSubmit,
    watch,
    formState: { errors, isSubmitting },
  } = useForm<Values>({ resolver: zodResolver(schema), defaultValues: { password: "", confirm: "" } });
  const password = watch("password");
  const [done, setDone] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!token)
    return (
      <div>
        <AuthHeading title="This link is incomplete" subtitle="Open the reset link from your email again, or request a new one." />
        <Button variant="secondary" size="lg" className="w-full" asChild>
          <Link to="/forgot">Request a new link</Link>
        </Button>
      </div>
    );

  if (done)
    return (
      <div>
        <span className={cn("mb-5 flex size-10 animate-indicator-in items-center justify-center rounded-lg", tone.green.chip)}>
          <Check className="size-[18px]" strokeWidth={2.5} />
        </span>
        <AuthHeading title="Password updated" subtitle="You were signed out everywhere. Log in with your new password." />
        <Button variant="primary" size="lg" className="w-full" asChild>
          <Link to="/login">Log in</Link>
        </Button>
      </div>
    );

  const submit = handleSubmit(async ({ password }) => {
    setError(null);
    try {
      await api.auth.reset(token, password);
      setDone(true);
    } catch (err) {
      if (isApiError(err) && (err.code === "invalid_token" || err.code === "not_found")) setError("This reset link has expired or was already used. Request a new one.");
      else setError(errorMessage(err));
    }
  });

  return (
    <div>
      <AuthHeading title="Choose a new password" subtitle="Use 12 or more characters. A short sentence works well." />
      {error && <Notice className="mb-4">{error}</Notice>}
      <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
        <AuthField label="New password">
          {(id) => (
            <div className="flex flex-col gap-2">
              <PasswordInput id={id} autoFocus autoComplete="new-password" invalid={!!errors.password} {...register("password")} />
              {errors.password ? <FieldError message={errors.password.message} /> : <StrengthMeter password={password} />}
            </div>
          )}
        </AuthField>
        <AuthField label="Confirm password">
          {(id) => (
            <div className="flex flex-col gap-1.5">
              <PasswordInput id={id} autoComplete="new-password" invalid={!!errors.confirm} {...register("confirm")} />
              <FieldError message={errors.confirm?.message} />
            </div>
          )}
        </AuthField>
        <Button type="submit" variant="primary" size="lg" className="mt-1 w-full" loading={isSubmitting}>
          Update password
        </Button>
      </form>
    </div>
  );
}
