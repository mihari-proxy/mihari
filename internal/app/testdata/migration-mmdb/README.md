# MMDB test fixtures

Copyright 2013–2026 MaxMind, Inc. These synthetic test databases are distributed under the accompanying MIT license. They are not production geolocation data.

Source is [MaxMind-DB commit 40d0b4ff0ffdad191e83bd8045b780dd052650e0](https://github.com/maxmind/MaxMind-DB/tree/40d0b4ff0ffdad191e83bd8045b780dd052650e0), the `test-data` gitlink used by the existing maxminddb-golang/v2 v2.4.1 dependency.

| Local file | Upstream file under `test-data/` | Bytes | SHA256 |
| --- | --- | ---: | --- |
| country.mmdb | GeoIP2-Country-Test.mmdb | 19492 | b37601903448683d241af52893c8cbf0fed461e0cdebe0bfaca01891fdeb6db9 |
| asn.mmdb | GeoLite2-ASN-Test.mmdb | 12653 | 75901b98ed6e58d3bd41af9985044b747a7ec0be1369f930c24f5e044427181a |

Migration tests use these synthetic country/ASN artifacts to verify type validation and preservation. No test fetches them from the network.
