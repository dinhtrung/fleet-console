/** An organization's mark.
 *
 *  These are the generated mock marks in `public/mock/` — synthetic, attributed in
 *  `docs/design/mock-assets.md`, and never a real company's logo. A raster that fails to load must
 *  not leave a hole in a row, so the component degrades to the monogram: the directory stays
 *  scannable with or without the images.
 *
 *  The image is decorative (`alt=""`) because the organization's name is always adjacent text;
 *  announcing it twice would be noise.
 */

import { useState } from "react";

function monogramOf(name: string): string {
  return name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((word) => word[0] ?? "")
    .join("")
    .toUpperCase();
}

export function OrgMark({ slug, name, size = 22 }: { slug: string; name: string; size?: number }) {
  const [missing, setMissing] = useState(false);

  if (missing) {
    return (
      <span
        aria-hidden="true"
        className="inline-flex shrink-0 items-center justify-center rounded-chip border border-rule-strong bg-ground font-semibold text-ink-muted"
        style={{ width: size, height: size, fontSize: Math.round(size * 0.42) }}
      >
        {monogramOf(name)}
      </span>
    );
  }

  return (
    <img
      src={`/mock/${slug}.png`}
      alt=""
      width={size}
      height={size}
      loading="lazy"
      decoding="async"
      onError={() => setMissing(true)}
      className="shrink-0 rounded-chip border border-rule bg-ground"
    />
  );
}
