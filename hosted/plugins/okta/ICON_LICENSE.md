# Icon License and Attribution

This directory contains a third-party icon asset. The terms below apply to that
asset only, and not to the source code in this package, which is governed by
the BSD 2-Clause license at the root of this repository.

## okta.svg

| | |
|---|---|
| **File in this directory** | `okta.svg` (7890 bytes, 40×40, viewBox 0 0 256 256) |
| **SHA-256 of this file** | `63a9aa063d9c6be1abb27aa2cf789f47fa3ca94a3bad75bfa654cbabd1dba967` |
| **Artwork** | The Okta "aura" mark, as drawn by the upstream project |
| **Upstream source** | `gilbarbara/logos` — https://github.com/gilbarbara/logos |
| **Upstream file** | https://raw.githubusercontent.com/gilbarbara/logos/main/logos/okta-icon.svg |
| **Upstream commit** | `1b8f5561d14ba9b8cf760c0df2fe56338cdcdbb3` (2023-05-18) |
| **SHA-256 of upstream file** | `ca647b46a93bb322f9db6f388e5bab7bf8b4ebe9d52b065b27007a2577dd370a` (8076 bytes) |
| **Author** | Gil Barbara and contributors |
| **License** | Creative Commons CC0 1.0 Universal (Public Domain Dedication) |
| **License text** | https://creativecommons.org/publicdomain/zero/1.0/legalcode |
| **Recorded** | 2026-10-13 |

### Why this source rather than Okta's own logo pack

Okta publishes a logo pack in its press room
(https://www.okta.com/press-room/media-assets/logos/) under the Terms of Use for
Okta Content (https://www.okta.com/terms-of-use-for-okta-content/, dated
2025-06-03). Those terms permit any person or entity to use the content, but
only "as it is provided" and with no modification "in any way", and the pack
checked on 2026-10-13 (`logos-09-2026.zip`) contains PNG lockups and stacks of
the mark with the wordmark, and no SVG and no standalone mark. A plugin icon
has to be an SVG of the mark alone, and producing one from that pack would mean
tracing and cropping it, which the terms forbid. Okta's Brandfolder portal
requires a login and publishes no terms.

The file here is therefore a public domain drawing of the mark rather than
Okta's own file, and it is carried under CC0 and the trademark notice below
rather than under the Okta Content terms. It was checked against the official
pack to confirm it depicts the current mark.

### Modifications

The file here is not byte-identical to the upstream one and its checksum will
not match it. It is the same path data presented differently:

- The XML declaration, the `<title>` element, the wrapping `<g>`, the unused
  `xlink` namespace and `preserveAspectRatio` were removed, and the
  whitespace in the path data was collapsed. Nothing about the geometry
  changed: every coordinate is as the upstream file has it.
- The root is sized `40×40` with the upstream `0 0 256 256` coordinate space
  kept as the `viewBox`, so it renders exactly as the upstream file does.
- The fill is `currentColor` rather than the fixed `#000000`. Okta supplies
  the mark in black and in white and expects whichever contrasts with the
  background; `currentColor` is how an SVG says the same thing.

It carries no metadata of any kind — no XML declaration, no `<metadata>`,
`<title>` or `<desc>`, no comments, no editor namespaces, no `id` and no C2PA
or other provenance manifest. Every element in it is a rendering instruction.
The checksum above is therefore of the artwork alone, and this document is the
only record of where it came from.

CC0 places the work in the public domain to the fullest extent permitted by
law. No attribution is required, no notice must be retained, and modification is
permitted without condition. This file is provided as a record of provenance
rather than to satisfy an obligation.

## Trademark notice

CC0 covers the file as a creative work. It does not, and cannot, grant any
right in the mark the file depicts.

"Okta" and the Okta logo are trademarks or registered trademarks of Okta, Inc.
in the United States and other countries. Gravwell is not affiliated with,
sponsored by or endorsed by Okta, Inc., and this icon does not imply otherwise.
It is used nominatively: it appears next to the Okta hosted ingester, a Gravwell
component that reads from the Okta API, to identify the service that component
connects to. It is not used as a badge, in a product name, or in any way that
suggests Okta's involvement in or approval of Gravwell. The Okta Terms of
Service reserve Okta's trademarks and trade dress, and Okta's trademark team
can be reached at trademarks@okta.com.

## Full text of the CC0 1.0 Universal dedication

CC0 1.0 Universal

Statement of Purpose

The laws of most jurisdictions throughout the world automatically confer
exclusive Copyright and Related Rights (defined below) upon the creator and
subsequent owner(s) (each and all, an "owner") of an original work of
authorship and/or a database (each, a "Work").

Certain owners wish to permanently relinquish those rights to a Work for the
purpose of contributing to a commons of creative, cultural and scientific
works ("Commons") that the public can reliably and without fear of later
claims of infringement build upon, modify, incorporate in other works, reuse
and redistribute as freely as possible in any form whatsoever and for any
purposes, including without limitation commercial purposes. These owners may
contribute to the Commons to promote the ideal of a free culture and the
further production of creative, cultural and scientific works, or to gain
reputation or greater distribution for their Work in part through the use and
efforts of others.

For these and/or other purposes and motivations, and without any expectation
of additional consideration or compensation, the person associating CC0 with a
Work (the "Affirmer"), to the extent that he or she is an owner of Copyright
and Related Rights in the Work, voluntarily elects to apply CC0 to the Work
and publicly distribute the Work under its terms, with knowledge of his or her
Copyright and Related Rights in the Work and the meaning and intended legal
effect of CC0 on those rights.

1. Copyright and Related Rights. A Work made available under CC0 may be
protected by copyright and related or neighboring rights ("Copyright and
Related Rights"). Copyright and Related Rights include, but are not limited
to, the following:

  i. the right to reproduce, adapt, distribute, perform, display, communicate,
  and translate a Work;

  ii. moral rights retained by the original author(s) and/or performer(s);

  iii. publicity and privacy rights pertaining to a person's image or likeness
  depicted in a Work;

  iv. rights protecting against unfair competition in regards to a Work,
  subject to the limitations in paragraph 4(a), below;

  v. rights protecting the extraction, dissemination, use and reuse of data in
  a Work;

  vi. database rights (such as those arising under Directive 96/9/EC of the
  European Parliament and of the Council of 11 March 1996 on the legal
  protection of databases, and under any national implementation thereof,
  including any amended or successor version of such directive); and

  vii. other similar, equivalent or corresponding rights throughout the world
  based on applicable law or treaty, and any national implementations thereof.

2. Waiver. To the greatest extent permitted by, but not in contravention of,
applicable law, Affirmer hereby overtly, fully, permanently, irrevocably and
unconditionally waives, abandons, and surrenders all of Affirmer's Copyright
and Related Rights and associated claims and causes of action, whether now
known or unknown (including existing as well as future claims and causes of
action), in the Work (i) in all territories worldwide, (ii) for the maximum
duration provided by applicable law or treaty (including future time
extensions), (iii) in any current or future medium and for any number of
copies, and (iv) for any purpose whatsoever, including without limitation
commercial, advertising or promotional purposes (the "Waiver"). Affirmer makes
the Waiver for the benefit of each member of the public at large and to the
detriment of Affirmer's heirs and successors, fully intending that such Waiver
shall not be subject to revocation, rescission, cancellation, termination, or
any other legal or equitable action to disrupt the quiet enjoyment of the Work
by the public as contemplated by Affirmer's express Statement of Purpose.

3. Public License Fallback. Should any part of the Waiver for any reason be
judged legally invalid or ineffective under applicable law, then the Waiver
shall be preserved to the maximum extent permitted taking into account
Affirmer's express Statement of Purpose. In addition, to the extent the Waiver
is so judged Affirmer hereby grants to each affected person a royalty-free,
non transferable, non sublicensable, non exclusive, irrevocable and
unconditional license to exercise Affirmer's Copyright and Related Rights in
the Work (i) in all territories worldwide, (ii) for the maximum duration
provided by applicable law or treaty (including future time extensions), (iii)
in any current or future medium and for any number of copies, and (iv) for any
purpose whatsoever, including without limitation commercial, advertising or
promotional purposes (the "License"). The License shall be deemed effective as
of the date CC0 was applied by Affirmer to the Work. Should any part of the
License for any reason be judged legally invalid or ineffective under
applicable law, such partial invalidity or ineffectiveness shall not
invalidate the remainder of the License, and in such case Affirmer hereby
affirms that he or she will not (i) exercise any of his or her remaining
Copyright and Related Rights in the Work or (ii) assert any associated claims
and causes of action with respect to the Work, in either case contrary to
Affirmer's express Statement of Purpose.

4. Limitations and Disclaimers.

  a. No trademark or patent rights held by Affirmer are waived, abandoned,
  surrendered, licensed or otherwise affected by this document.

  b. Affirmer offers the Work as-is and makes no representations or warranties
  of any kind concerning the Work, express, implied, statutory or otherwise,
  including without limitation warranties of title, merchantability, fitness
  for a particular purpose, non infringement, or the absence of latent or
  other defects, accuracy, or the present or absence of errors, whether or not
  discoverable, all to the greatest extent permissible under applicable law.

  c. Affirmer disclaims responsibility for clearing rights of other persons
  that may apply to the Work or any use thereof, including without limitation
  any person's Copyright and Related Rights in the Work. Further, Affirmer
  disclaims responsibility for obtaining any necessary consents, permissions
  or other rights required for any use of the Work.

  d. Affirmer understands and acknowledges that Creative Commons is not a
  party to this document and has no duty or obligation with respect to this
  CC0 or use of the Work.

For more information, please see
<http://creativecommons.org/publicdomain/zero/1.0/>
