# Icon License and Attribution

This directory contains a third-party icon asset. The terms below apply to that
asset only, and not to the source code in this package, which is governed by
the BSD 2-Clause license at the root of this repository.

## jamf.svg

| | |
|---|---|
| **File in this directory** | `jamf.svg` (2839 bytes, 108×113) |
| **SHA-256 of this file** | `add421742d45f7695fdc53c2ce4617fedfa4fbd1bbc754ad2480c345c8dcb2a5` |
| **Artwork** | The Jamf corporate logo, stacked layout, full colour on light ("Jamf stacked color dark") |
| **Upstream source** | Jamf media kit, https://www.jamf.com/about/media-kit/ |
| **Upstream collection** | "Jamf logo collection - All Jamf logos", https://jamf.bynder.com/web/298f12a3b9f99b8a/jamf-logos/ |
| **Upstream asset** | `2567f4a4-503c-4035-8d6b-c24fa9a99418`, version `de153614`, asset type Branding / Logo, year 2025 |
| **Upstream usage rights** | Marked "Approved for External Use" in the collection |
| **Owner** | JAMF Software, LLC |
| **Terms** | Jamf API License Agreement, section 9 (Trademark Use), together with the Jamf Brand Style Guide for Partners |
| **Terms text** | https://developer.jamf.com/developer-guide/docs/api-license-agreement (last updated 2026-09-08); https://resources.jamf.com/documents/services/jamf-brand-style-guide-for-partners.pdf |
| **Recorded** | 2026-10-13 |

### Why we may carry it

This plugin is an "Application" in the sense of the Jamf API License Agreement:
"the software, integration, product, service, interface or other application
that you develop that interoperates with the Jamf APIs". The agreement binds
"the individual or entity accessing or using the Jamf APIs", which this plugin
does, and its section 9 grants:

> You may use Jamf's Trademarks solely in connection with your promotion and
> distribution of your Application only.

subject to the owner's "then-current usage guidelines". The grant lasts for the
term of the agreement, which Jamf may terminate at any time; if it is terminated
this file has to be removed along with the API access it accompanies.

The usage guidelines are the Jamf Brand Style Guide for Partners. The rules
there that shape this file are:

- "never attempt to re-create it. Always use approved artwork", which is why
  the file is Jamf's own SVG rather than a drawing of the mark;
- the icon and wordmark "should remain in a fixed relationship and not
  separated or altered" and "may be used separately only by Jamf", which is
  why this is the full logo rather than the square icon that would otherwise
  have matched the other plugin icons;
- "do not alter logo colors", "do not distort or change proportions", which is
  why the file is carried verbatim and why it does not take the page's colour
  the way an icon with a `currentColor` fill would.

Showing the logo beside the Jamf hosted ingester, a Gravwell component that
reads from the Jamf Pro API, to identify the service it connects to, is use in
connection with the distribution of that Application. It is not used as a
badge, in a product name, or in any way that suggests Jamf's involvement in or
approval of Gravwell.

### Modifications

None. The file is byte-identical to the asset downloaded from the collection
above, and the checksum is of that asset. It carries no metadata of any kind —
no XML declaration, no `<metadata>`, `<title>` or `<desc>`, no comments, no
editor namespaces, no `id` and no C2PA or other provenance manifest. Every
element in it is a rendering instruction.

The configuration interface strips the root `width` and `height` when it draws
the icon so that the stylesheet decides its size; the `viewBox` is kept, so the
drawing's proportions are exactly as supplied. That happens at render time and
does not change this file.

## Trademark notice

"Jamf" and the Jamf logo are registered or common law trademarks of JAMF
Software, LLC in the United States and other countries. Gravwell is not
affiliated with, sponsored by or endorsed by Jamf, and this logo does not imply
otherwise. Jamf's Terms of Use reserve its trademarks and logos, and the only
right relied on here is the one quoted above from the Jamf API License
Agreement. Questions about Jamf trademark usage go to Jamf; this file is not
licensed for any other use and may not be lifted out of this repository on the
strength of this record.
