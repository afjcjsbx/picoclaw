import { useTranslation } from "react-i18next"

import type { ChannelConfig } from "@/api/channels"
import {
  type ArrayFieldFlusher,
  ChannelArrayListField,
} from "@/components/channels/channel-array-list-field"
import { asStringArray } from "@/components/channels/channel-array-utils"
import { Field, SwitchCardField } from "@/components/shared-form"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

import { StreamingConfigField } from "./streaming-config-field"

interface A2AFormProps {
  config: ChannelConfig
  onChange: (key: string, value: unknown) => void
  configuredSecrets: string[]
  fieldErrors?: Record<string, string>
  registerArrayFieldFlusher?: (
    fieldPath: string,
    flusher: ArrayFieldFlusher | null,
  ) => void
  arrayFieldResetVersion?: number
}

function asString(value: unknown): string {
  return typeof value === "string" ? value : ""
}

function numberInputValue(value: unknown): string {
  return typeof value === "number" ? String(value) : ""
}

function isLoopbackHost(host: string): boolean {
  const normalized = host.trim().toLowerCase()
  return (
    normalized === "localhost" ||
    normalized === "::1" ||
    /^127(?:\.\d{1,3}){3}$/.test(normalized)
  )
}

export function A2AForm({
  config,
  onChange,
  configuredSecrets,
  fieldErrors = {},
  registerArrayFieldFlusher,
  arrayFieldResetVersion,
}: A2AFormProps) {
  const { t } = useTranslation()
  const tokens = asStringArray(config._tokens)
  const tokensConfigured = configuredSecrets.includes("tokens")
  const tokensCleared = config._tokens_cleared === true
  const hasTokens = tokens.length > 0 || (tokensConfigured && !tokensCleared)
  const requiresTokens = !isLoopbackHost(asString(config.host)) && !hasTokens

  const handleTokensChange = (nextTokens: string[]) => {
    onChange("_tokens", nextTokens)
    if (tokensCleared) {
      onChange("_tokens_cleared", false)
    }
  }

  const handleClearTokens = () => {
    onChange("_tokens", [])
    onChange("_tokens_cleared", true)
  }

  return (
    <div className="space-y-6">
      <Card className="shadow-sm">
        <CardHeader className="border-border/60 border-b px-6 py-4">
          <CardTitle className="text-foreground text-sm font-medium">
            {t("channels.a2a.listenerTitle")}
          </CardTitle>
          <CardDescription>{t("channels.a2a.listenerDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="divide-border/60 divide-y px-6 py-0 [&>div]:py-5">
          <Field
            label={t("channels.a2a.host")}
            required
            hint={t("channels.a2a.hostHint")}
            error={fieldErrors.host}
          >
            <Input
              value={asString(config.host)}
              onChange={(event) => onChange("host", event.target.value)}
              placeholder="127.0.0.1"
              aria-label={t("channels.a2a.host")}
            />
          </Field>
          <Field
            label={t("channels.a2a.port")}
            hint={t("channels.a2a.portHint")}
            error={fieldErrors.port}
          >
            <Input
              type="number"
              min={1}
              max={65535}
              value={numberInputValue(config.port)}
              onChange={(event) =>
                onChange(
                  "port",
                  event.target.value === "" ? 0 : Number(event.target.value),
                )
              }
              placeholder="9900"
              aria-label={t("channels.a2a.port")}
            />
          </Field>
          <Field
            label={t("channels.a2a.publicUrl")}
            hint={t("channels.a2a.publicUrlHint")}
            error={fieldErrors.url}
          >
            <Input
              type="url"
              value={asString(config.url)}
              onChange={(event) => onChange("url", event.target.value)}
              placeholder="https://agent.example.com"
              aria-label={t("channels.a2a.publicUrl")}
            />
          </Field>
        </CardContent>
      </Card>

      <Card className="shadow-sm">
        <CardHeader className="border-border/60 border-b px-6 py-4">
          <CardTitle className="text-foreground text-sm font-medium">
            {t("channels.a2a.agentCardTitle")}
          </CardTitle>
          <CardDescription>{t("channels.a2a.agentCardDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="divide-border/60 divide-y px-6 py-0 [&>div]:py-5">
          <Field
            label={t("channels.a2a.name")}
            hint={t("channels.a2a.nameHint")}
            error={fieldErrors.name}
          >
            <Input
              value={asString(config.name)}
              onChange={(event) => onChange("name", event.target.value)}
              placeholder="PicoClaw"
              aria-label={t("channels.a2a.name")}
            />
          </Field>
          <Field
            label={t("channels.a2a.description")}
            hint={t("channels.a2a.descriptionHint")}
            error={fieldErrors.description}
          >
            <Textarea
              value={asString(config.description)}
              onChange={(event) => onChange("description", event.target.value)}
              placeholder={t("channels.a2a.descriptionPlaceholder")}
              aria-label={t("channels.a2a.description")}
            />
          </Field>
        </CardContent>
      </Card>

      <Card className="shadow-sm">
        <CardHeader className="border-border/60 border-b px-6 py-4">
          <CardTitle className="text-foreground text-sm font-medium">
            {t("channels.a2a.authenticationTitle")}
          </CardTitle>
          <CardDescription>
            {t("channels.a2a.authenticationDesc")}
          </CardDescription>
        </CardHeader>
        <CardContent className="divide-border/60 divide-y px-6 py-0 [&>div]:py-5">
          {tokensConfigured && !tokensCleared && (
            <div className="flex flex-wrap items-center justify-between gap-3">
              <p className="text-muted-foreground text-sm">
                {t("channels.a2a.tokensConfigured")}
              </p>
              <Button variant="outline" size="sm" onClick={handleClearTokens}>
                {t("channels.a2a.clearTokens")}
              </Button>
            </div>
          )}
          {tokensCleared && (
            <p className="text-destructive text-sm">
              {t("channels.a2a.tokensWillBeCleared")}
            </p>
          )}
          {requiresTokens && (
            <p className="text-destructive text-sm">
              {t("channels.a2a.tokensRequired")}
            </p>
          )}
          <ChannelArrayListField
            label={t("channels.a2a.tokens")}
            hint={t("channels.a2a.tokensHint")}
            error={fieldErrors.tokens}
            value={tokens}
            onChange={handleTokensChange}
            placeholder={t("channels.a2a.tokenPlaceholder")}
            fieldPath="_tokens"
            registerFlusher={registerArrayFieldFlusher}
            resetVersion={arrayFieldResetVersion}
            secret
          />
        </CardContent>
      </Card>

      <Card className="shadow-sm">
        <CardHeader className="border-border/60 border-b px-6 py-4">
          <CardTitle className="text-foreground text-sm font-medium">
            {t("channels.a2a.limitsTitle")}
          </CardTitle>
          <CardDescription>{t("channels.a2a.limitsDesc")}</CardDescription>
        </CardHeader>
        <CardContent className="divide-border/60 divide-y px-6 py-0 [&>div]:py-5">
          <Field
            label={t("channels.a2a.maxBodyBytes")}
            hint={t("channels.a2a.maxBodyBytesHint")}
            error={fieldErrors.max_body_bytes}
          >
            <Input
              type="number"
              min={0}
              value={numberInputValue(config.max_body_bytes)}
              onChange={(event) =>
                onChange(
                  "max_body_bytes",
                  event.target.value === "" ? 0 : Number(event.target.value),
                )
              }
              placeholder="1048576"
              aria-label={t("channels.a2a.maxBodyBytes")}
            />
          </Field>
          <Field
            label={t("channels.a2a.rateLimitPerMinute")}
            hint={t("channels.a2a.rateLimitPerMinuteHint")}
            error={fieldErrors.rate_limit_per_minute}
          >
            <Input
              type="number"
              min={0}
              value={numberInputValue(config.rate_limit_per_minute)}
              onChange={(event) =>
                onChange(
                  "rate_limit_per_minute",
                  event.target.value === "" ? 0 : Number(event.target.value),
                )
              }
              placeholder="60"
              aria-label={t("channels.a2a.rateLimitPerMinute")}
            />
          </Field>
          <Field
            label={t("channels.a2a.maxTasks")}
            hint={t("channels.a2a.maxTasksHint")}
            error={fieldErrors.max_tasks}
          >
            <Input
              type="number"
              min={0}
              value={numberInputValue(config.max_tasks)}
              onChange={(event) =>
                onChange(
                  "max_tasks",
                  event.target.value === "" ? 0 : Number(event.target.value),
                )
              }
              placeholder="256"
              aria-label={t("channels.a2a.maxTasks")}
            />
          </Field>
          <Field
            label={t("channels.a2a.maxConcurrent")}
            hint={t("channels.a2a.maxConcurrentHint")}
            error={fieldErrors.max_concurrent}
          >
            <Input
              type="number"
              min={0}
              value={numberInputValue(config.max_concurrent)}
              onChange={(event) =>
                onChange(
                  "max_concurrent",
                  event.target.value === "" ? 0 : Number(event.target.value),
                )
              }
              placeholder="4"
              aria-label={t("channels.a2a.maxConcurrent")}
            />
          </Field>
          <Field
            label={t("channels.a2a.taskTimeoutSeconds")}
            hint={t("channels.a2a.taskTimeoutSecondsHint")}
            error={fieldErrors.task_timeout_seconds}
          >
            <Input
              type="number"
              min={0}
              value={numberInputValue(config.task_timeout_seconds)}
              onChange={(event) =>
                onChange(
                  "task_timeout_seconds",
                  event.target.value === "" ? 0 : Number(event.target.value),
                )
              }
              placeholder="300"
              aria-label={t("channels.a2a.taskTimeoutSeconds")}
            />
          </Field>
          <SwitchCardField
            label={t("channels.a2a.pushAllowPrivate")}
            hint={t("channels.a2a.pushAllowPrivateHint")}
            checked={config.push_allow_private === true}
            onCheckedChange={(checked) =>
              onChange("push_allow_private", checked)
            }
            ariaLabel={t("channels.a2a.pushAllowPrivate")}
          />
        </CardContent>
      </Card>

      <Card className="shadow-sm">
        <CardContent className="px-6 py-0 [&>div]:py-5">
          <StreamingConfigField
            value={config.streaming}
            onChange={(value) => onChange("streaming", value)}
          />
        </CardContent>
      </Card>
    </div>
  )
}
