# TODO

- [ ] **Slice flags comma-split their values.** `--var`, `--to`, `--cc`, `--bcc`
      and `--attach` use urfave's `StringSliceFlag`, which splits each value on
      commas. So `--var "Address=Stuttgart, DE"` becomes two entries, and a
      recipient display name like `"Doe, John <j@x.com>"` is split incorrectly.
      Fix: use a non-splitting repeatable flag for `--var` (and reconsider
      comma-splitting on the address flags), or document the limitation.
