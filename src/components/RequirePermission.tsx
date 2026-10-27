/** Permission gate for a whole surface.
 *
 *  A route the identity may not open renders an explanation of which permission is missing — never a
 *  blank screen and never a broken one. The navigation already hides these sections; this catches the
 *  case where someone pastes an address. */

import type { ReactNode } from "react";
import { useAccess } from "../access/access";
import { DeniedState } from "./ui";
import { PageHeader } from "../shell/PageHeader";

export function RequirePermission({
  permission,
  what,
  title,
  children,
}: {
  permission: string;
  what: string;
  title: string;
  children: ReactNode;
}) {
  const { can } = useAccess();
  if (can(permission)) return <>{children}</>;
  return (
    <>
      <PageHeader title={title} crumbs={[{ label: "Board", to: "/" }, { label: title }]} />
      <div className="px-3 sm:px-5">
        <DeniedState permission={permission} what={what} />
      </div>
    </>
  );
}
