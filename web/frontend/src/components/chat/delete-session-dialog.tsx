import { IconLoader2 } from "@tabler/icons-react"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"

import type { SessionSummary } from "@/api/sessions"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"

interface DeleteSessionDialogProps {
  session: SessionSummary | null
  onClose: () => void
  onDelete: (id: string) => Promise<boolean>
  onDeleted: (id: string) => void
}

export function DeleteSessionDialog({
  session,
  onClose,
  onDelete,
  onDeleted,
}: DeleteSessionDialogProps) {
  const { t } = useTranslation()
  const [deleting, setDeleting] = useState(false)

  useEffect(() => {
    if (!session) setDeleting(false)
  }, [session])

  const handleConfirm = async () => {
    if (!session || deleting) return
    setDeleting(true)
    try {
      if (await onDelete(session.id)) {
        onDeleted(session.id)
      }
      onClose()
    } finally {
      setDeleting(false)
    }
  }

  return (
    <AlertDialog
      open={session !== null}
      onOpenChange={(open) => {
        if (!open && !deleting) onClose()
      }}
    >
      <AlertDialogContent size="sm">
        <AlertDialogHeader>
          <AlertDialogTitle>{t("chat.deleteSessionTitle")}</AlertDialogTitle>
          <AlertDialogDescription>
            {t("chat.deleteSessionConfirm", { title: session?.title ?? "" })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={deleting}>
            {t("common.cancel")}
          </AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={deleting}
            onClick={(event) => {
              event.preventDefault()
              void handleConfirm()
            }}
          >
            {deleting && <IconLoader2 className="size-4 animate-spin" />}
            {t("chat.deleteSession")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
