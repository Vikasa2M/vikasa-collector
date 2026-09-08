# Changelog

## [0.1.0](https://github.com/Vikasa2M/vikasa-collector/compare/v0.0.1...v0.1.0) (2026-09-08)


### ⚠ BREAKING CHANGES

* openits.perception.zone-interval-report.v1 no longer carries observed-count or occupancy-percent; consumers that read either must subscribe to openits.zone-occupancy.zone-occupancy-interval-report.v1. Every perception payload's object-class identityref changes module prefix.

### Features

* adopt openits-models v0.3.0 and map the zone-occupancy service ([ece05e6](https://github.com/Vikasa2M/vikasa-collector/commit/ece05e66cea4d0a0657d798c80a657d12bb2bb1f))
* adopt openits-models v0.4.0 ([675d865](https://github.com/Vikasa2M/vikasa-collector/commit/675d865c44ff81935dca1a77cfd0dabb070caded))
* CloudEvents binary mode, ULID ce-id, and the profile source URN ([a724d76](https://github.com/Vikasa2M/vikasa-collector/commit/a724d76032663c8e0b464138d343a2c44242a358))
* **dev:** add `make dev`, and route contributors to the bar they are judged against ([7b4a89e](https://github.com/Vikasa2M/vikasa-collector/commit/7b4a89e90cbd9fab4de9e7bec1f75ad864fb691c))
* **dms:** carry the face message and environment cluster, and map the v0.4.0 DMS event surface ([a0dac37](https://github.com/Vikasa2M/vikasa-collector/commit/a0dac37eb83dca40e714648b6c398751cb941a69))
* model cctv control mode and tour state ([7fc5ace](https://github.com/Vikasa2M/vikasa-collector/commit/7fc5ace569fe48a69987e15d0ab1892b7f38ec98))
* model perception zone interval reports ([248917f](https://github.com/Vikasa2M/vikasa-collector/commit/248917f45d869efc9e65434192b902fb322a5acd))
* model perception zone-incident lifecycle end to end ([fdec971](https://github.com/Vikasa2M/vikasa-collector/commit/fdec971920614dda3bed973e1ddf2e29cc0f5445))
* model traffic-sensor interval reports end to end ([63d1dbf](https://github.com/Vikasa2M/vikasa-collector/commit/63d1dbfa55312d0c484139933956080480fc05b2))
* **ntcip:** add ntcip-dms adapter for NTCIP 1203 signs ([391ab64](https://github.com/Vikasa2M/vikasa-collector/commit/391ab64e38b12f4a99ae9f5832d4e819cce4a342))
* **ntcip:** adopt openits-models v0.4.0 for DMS wire surface ([b60b4b3](https://github.com/Vikasa2M/vikasa-collector/commit/b60b4b3ad6d2293e496aaf509adb8b334c2989d9))
* pin openits-models on release tags, enforced by lint Rule D ([b6241f8](https://github.com/Vikasa2M/vikasa-collector/commit/b6241f84c51813cf80e7c7802363d2b3a3afb857))
* provision a stream per namespace and keep health off the catalog space ([ea56655](https://github.com/Vikasa2M/vikasa-collector/commit/ea566553d3bc27a5a34a8111a385d84b97e4db74))
* **subject:** adopt the profile's seven-token grammar as the default ([e9f3640](https://github.com/Vikasa2M/vikasa-collector/commit/e9f36406160a2792c6f014cfdfdf4c1e82d2b87f))
* **subject:** root subjects on the ce-type namespace ([f0ff037](https://github.com/Vikasa2M/vikasa-collector/commit/f0ff037bfc791d6b4a89c502b5dc564c208d51c8))
* wire the openits emitter into the chain behind a required collector_id ([1e4f144](https://github.com/Vikasa2M/vikasa-collector/commit/1e4f14497f5aaccb45362741ec48dec28520cf86))
* **wire:** carry ce-dataschema pinned to the defining events module ([bd97d31](https://github.com/Vikasa2M/vikasa-collector/commit/bd97d317f48d11d4c8291ef0b4b59ffe34e85ef8))
* **wire:** complete the domain-to-ce-type mapping table ([3992d7e](https://github.com/Vikasa2M/vikasa-collector/commit/3992d7ea5cc82b86a5a864de0e6c6d4f4fdc6563))
* **wire:** extend the shared fault family to cctv, traffic-sensor and perception ([f358b03](https://github.com/Vikasa2M/vikasa-collector/commit/f358b0317d00afffad1478d98748dc804c423bad))
* **wire:** map both DMS mode axes onto the shared ce-type ([16cec8f](https://github.com/Vikasa2M/vikasa-collector/commit/16cec8f6aa45309de56c23a3da354a17b320c19a))
* **wire:** map controller modes to upstream identities ([034f56b](https://github.com/Vikasa2M/vikasa-collector/commit/034f56bc59648049aec316def364a798fbdcf7f3))
* **wire:** map the shared fault events across both services ([bca986c](https://github.com/Vikasa2M/vikasa-collector/commit/bca986c90219e4a84e680c32c98d0d5840235184))
* **wire:** openits-models emitter skeleton with tuple dispatch ([780d4e2](https://github.com/Vikasa2M/vikasa-collector/commit/780d4e2f6727c353cd43a63068376f88db8003b9))
* **wire:** populate the mandatory event-header leaves ([e50e629](https://github.com/Vikasa2M/vikasa-collector/commit/e50e629aaccedc12ca16f3cd5b30cae31d56dd93))


### Bug Fixes

* **ci:** enforce ADR 0010's never-replace clause ([5a25d71](https://github.com/Vikasa2M/vikasa-collector/commit/5a25d713ed0b794a5782241c392f7d8984579999))
* **ci:** lint relative links in .claude/skills, and fix the template's depth ([0495e8d](https://github.com/Vikasa2M/vikasa-collector/commit/0495e8d95f82de3481b53fe89b3be5d70024720d))
* **ci:** make three guards check the thing they claim ([10eb5e5](https://github.com/Vikasa2M/vikasa-collector/commit/10eb5e51058b8d1ab27f155af635bf33bffea231))
* **deps:** align go.mod with go.sum on gosnmp v1.44.0 ([3e76e52](https://github.com/Vikasa2M/vikasa-collector/commit/3e76e52f4e39350ffbddc9c9ebeebe9774918d40))
* **docs:** asyncapi addresses match the ADR 0011 grammar ([2da51f5](https://github.com/Vikasa2M/vikasa-collector/commit/2da51f5f016d189cc22489ffe066dfb6830da35f))
* **docs:** correct ce-source, ce-id and ce-type envelope descriptions ([09351ed](https://github.com/Vikasa2M/vikasa-collector/commit/09351edf6541801778faa2a17bd1b5ee4dba645b))
* **docs:** correct four false claims and document the command line ([c9a5588](https://github.com/Vikasa2M/vikasa-collector/commit/c9a558891d81fce1f08efb2a59ad6d1beec8d81f))
* **docs:** correct the ce-type count, guard inventory and tier list ([04ab5f0](https://github.com/Vikasa2M/vikasa-collector/commit/04ab5f049ec8c8d19a1a93f844fbf4c089f4f414))
* **docs:** correct the paths, citations and status lines review found ([7c70b9f](https://github.com/Vikasa2M/vikasa-collector/commit/7c70b9f99f1c6770003a98807af86454e9c863ef))
* **ntcip-dms:** harden partial reads and record environment fixture ([b9611b6](https://github.com/Vikasa2M/vikasa-collector/commit/b9611b6bf92207c6a6d87696d5631ed48c8a693b))
* **ntcip-dms:** keep env facet when humidity table is empty ([a49c759](https://github.com/Vikasa2M/vikasa-collector/commit/a49c759d4f7d8a84faca01f488fb66e556c07b23))
* **ntcip-dms:** reject malformed message identity ([fac5433](https://github.com/Vikasa2M/vikasa-collector/commit/fac5433ff93bec38e5ca19cddf14d6102c020adc))
