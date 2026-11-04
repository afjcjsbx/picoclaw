import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { getAppConfig } from "@/api/channels"
import { updateMCPRuntimeSettings } from "@/api/mcp"
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
import { showSaveSuccessOrRestartToast } from "@/lib/restart-required"
import { refreshGatewayState } from "@/store/gateway"

import {
  EMPTY_MCP_CONFIG_FORM,
  type MCPConfigForm,
  type MCPDiscoveryForm,
  buildMCPConfigForm,
  buildMCPDiscoveryPatch,
} from "./mcp-form"

export function MCPSettingsTab() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [form, setForm] = useState<MCPConfigForm>(EMPTY_MCP_CONFIG_FORM)
  const [baseline, setBaseline] = useState<MCPConfigForm>(EMPTY_MCP_CONFIG_FORM)

  const query = useQuery({
    queryKey: ["config"],
    queryFn: getAppConfig,
  })

  useEffect(() => {
    if (!query.data) return
    const parsed = buildMCPConfigForm(query.data)
    setForm(parsed)
    setBaseline(parsed)
  }, [query.data])

  const updateDiscovery = <K extends keyof MCPDiscoveryForm>(
    key: K,
    value: MCPDiscoveryForm[K],
  ) => {
    setForm((previous) => ({
      ...previous,
      discovery: { ...previous.discovery, [key]: value },
    }))
  }

  const dirty = JSON.stringify(form) !== JSON.stringify(baseline)

  const saveMutation = useMutation({
    mutationFn: () =>
      updateMCPRuntimeSettings({
        enabled: form.enabled,
        discovery: buildMCPDiscoveryPatch(form.discovery),
      }),
    onSuccess: async () => {
      setBaseline(form)
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["config"] }),
        queryClient.invalidateQueries({ queryKey: ["agents", "mcp"] }),
      ])
      const gateway = await refreshGatewayState({ force: true })
      showSaveSuccessOrRestartToast(
        t,
        t("pages.agent.mcp.settings.saved", "MCP settings saved."),
        t("navigation.mcp", "MCP"),
        gateway?.restartRequired === true,
      )
    },
    onError: (error) => {
      toast.error(
        error instanceof Error
          ? error.message
          : t(
              "pages.agent.mcp.settings.save_error",
              "Could not save MCP settings.",
            ),
      )
    },
  })

  return (
    <div className="flex-1 overflow-auto px-6 py-6 pb-20">
      <div className="animate-in fade-in mx-auto w-full max-w-4xl space-y-6 pt-2 duration-300">
        <div className="max-w-2xl space-y-2">
          <h1 className="text-foreground/90 text-2xl font-semibold tracking-tight">
            {t("pages.agent.mcp.settings.title", "MCP Settings")}
          </h1>
          <p className="text-muted-foreground/80 text-[14px] leading-relaxed">
            {t(
              "pages.agent.mcp.settings.description",
              "Configure the MCP integration and tool discovery without editing config.json.",
            )}
          </p>
        </div>

        {query.isLoading ? (
          <p className="text-muted-foreground text-sm">{t("labels.loading")}</p>
        ) : query.error ? (
          <div className="border-destructive/20 bg-destructive/5 text-destructive rounded-2xl border p-8 text-center text-sm">
            {t(
              "pages.agent.mcp.settings.load_error",
              "Failed to load MCP settings.",
            )}
          </div>
        ) : (
          <>
            <Card size="sm">
              <CardHeader className="border-border border-b">
                <CardTitle>
                  {t("pages.agent.mcp.settings.integration", "Integration")}
                </CardTitle>
                <CardDescription>
                  {t(
                    "pages.agent.mcp.settings.integration_hint",
                    "Turn the MCP runtime on or off for the gateway.",
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent className="pt-0">
                <div className="divide-border/70 divide-y">
                  <SwitchCardField
                    label={t("pages.agent.mcp.settings.enabled", "Enable MCP")}
                    hint={t(
                      "pages.agent.mcp.settings.enabled_hint",
                      "Turn MCP server integration on or off.",
                    )}
                    layout="setting-row"
                    checked={form.enabled}
                    disabled={saveMutation.isPending}
                    onCheckedChange={(checked) =>
                      setForm((previous) => ({
                        ...previous,
                        enabled: checked,
                      }))
                    }
                  />

                  {form.enabled && (
                    <>
                      <SwitchCardField
                        label={t(
                          "pages.agent.mcp.settings.discovery_enabled",
                          "Enable MCP discovery",
                        )}
                        hint={t(
                          "pages.agent.mcp.settings.discovery_enabled_hint",
                          "Allow MCP discovery tools to search registered servers.",
                        )}
                        layout="setting-row"
                        checked={form.discovery.enabled}
                        disabled={saveMutation.isPending}
                        onCheckedChange={(checked) =>
                          updateDiscovery("enabled", checked)
                        }
                      />

                      {form.discovery.enabled && (
                        <>
                          <Field
                            label={t(
                              "pages.agent.mcp.settings.discovery_ttl",
                              "Discovered tool unlock TTL",
                            )}
                            hint={t(
                              "pages.agent.mcp.settings.discovery_ttl_hint",
                              "How many tool-execution TTL ticks discovered tools remain available after search.",
                            )}
                            layout="setting-row"
                          >
                            <Input
                              type="number"
                              min={1}
                              value={form.discovery.ttl}
                              disabled={saveMutation.isPending}
                              onChange={(event) =>
                                updateDiscovery("ttl", event.target.value)
                              }
                            />
                          </Field>

                          <Field
                            label={t(
                              "pages.agent.mcp.settings.discovery_max_results",
                              "Discovery max results",
                            )}
                            hint={t(
                              "pages.agent.mcp.settings.discovery_max_results_hint",
                              "Maximum MCP discovery matches returned per query.",
                            )}
                            layout="setting-row"
                          >
                            <Input
                              type="number"
                              min={1}
                              value={form.discovery.maxResults}
                              disabled={saveMutation.isPending}
                              onChange={(event) =>
                                updateDiscovery(
                                  "maxResults",
                                  event.target.value,
                                )
                              }
                            />
                          </Field>

                          <SwitchCardField
                            label={t(
                              "pages.agent.mcp.settings.discovery_use_bm25",
                              "Use BM25 ranking",
                            )}
                            hint={t(
                              "pages.agent.mcp.settings.discovery_use_bm25_hint",
                              "Use BM25 lexical scoring for MCP discovery results.",
                            )}
                            layout="setting-row"
                            checked={form.discovery.useBM25}
                            disabled={
                              saveMutation.isPending ||
                              (form.discovery.useBM25 &&
                                !form.discovery.useRegex)
                            }
                            onCheckedChange={(checked) =>
                              updateDiscovery("useBM25", checked)
                            }
                          />

                          <SwitchCardField
                            label={t(
                              "pages.agent.mcp.settings.discovery_use_regex",
                              "Enable regex search",
                            )}
                            hint={t(
                              "pages.agent.mcp.settings.discovery_use_regex_hint",
                              "Allow regex-based matching in MCP discovery.",
                            )}
                            layout="setting-row"
                            checked={form.discovery.useRegex}
                            disabled={
                              saveMutation.isPending ||
                              (form.discovery.useRegex &&
                                !form.discovery.useBM25)
                            }
                            onCheckedChange={(checked) =>
                              updateDiscovery("useRegex", checked)
                            }
                          />
                        </>
                      )}
                    </>
                  )}
                </div>
              </CardContent>
            </Card>

            <div className="flex flex-col-reverse gap-3 sm:flex-row sm:items-center sm:justify-between">
              <p className="text-muted-foreground text-xs leading-relaxed">
                {t(
                  "pages.agent.mcp.settings.reload_hint",
                  "Restart or reload the gateway to apply changes to the running process.",
                )}
              </p>
              <div className="flex justify-end gap-2">
                <Button
                  type="button"
                  variant="outline"
                  disabled={!dirty || saveMutation.isPending}
                  onClick={() => setForm(baseline)}
                >
                  {t("common.reset", "Reset")}
                </Button>
                <Button
                  type="button"
                  disabled={!dirty || saveMutation.isPending}
                  onClick={() => saveMutation.mutate()}
                >
                  {saveMutation.isPending
                    ? t("common.saving", "Saving...")
                    : t("common.save", "Save")}
                </Button>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}
