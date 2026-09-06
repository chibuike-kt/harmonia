"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";

/**
 * Retired per ADR-005: Connected agents now lives inside the settings
 * modal, not as its own page. This route stays only so an existing
 * bookmark or external link still lands somewhere real — it hands off
 * to the shell via a query param, since this page renders outside the
 * shell and has no sidebar of its own to open the modal directly (see
 * Sidebar's own "?settings=" pickup effect).
 */
export default function ConnectAgentsRedirect() {
  const router = useRouter();

  useEffect(() => {
    router.replace("/dashboard?settings=agents");
  }, [router]);

  return null;
}
