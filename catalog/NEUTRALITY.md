# Catalog neutrality rules

These rules govern which alternatives enter the exitctl catalog and how they
are shown (FR-017). They apply to every contribution: maintainer, community,
vendor, and LLM-drafted entries alike.

## Entry criteria for alternatives

An alternative may be listed only when all of the following hold:

1. It is publicly available (open source, or a generally available commercial
   offering that anyone can sign up for).
2. It is actively maintained: a release, or a documented maintenance update,
   within the last 12 months.
3. A documented migration path exists from at least one service it is mapped
   to (official guide, compatible API or protocol, or a published tool).

## No paid placements

Listing, ordering, and recommendation cannot be bought. No payment, sponsorship,
or partnership influences whether or where an alternative appears.

## Vendor-contributed entries

Entries contributed by the vendor of the alternative (or on its behalf) are
accepted under the same criteria, are marked `vendor_contributed: true`, and
must be reviewed and approved by a catalog maintainer before they become
`verified`.

## Recommendations

- Only catalog maintainers (listed in `catalog.yaml` under `maintainers`) may
  set `recommended: true`, and each recommended mapping records the approving
  maintainer in `approved_by`.
- `recommended` only affects display order and markers. It never affects any
  score, grade, or lever calculation.
