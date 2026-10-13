<p align="center"><a href="README.md">English</a> · <b>한국어</b></p>

<div align="center">
  <h1>cliproxyapi-plugin-quota-monitor</h1>
  <p>CLIProxyAPI를 위한 공급자 계정 쿼터·크레딧 모니터링 및 반응형 대시보드(한국어/영어) 네이티브 Go 플러그인.</p>

  <p>
    <a href="https://go.dev" target="_blank" rel="noreferrer"><img src="https://img.shields.io/badge/Go-1.26.5-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.26.5" /></a>
    <a href="https://github.com/dohwi/cliproxyapi-plugin-quota-monitor/actions/workflows/verify.yml" target="_blank" rel="noreferrer"><img src="https://img.shields.io/github/actions/workflow/status/dohwi/cliproxyapi-plugin-quota-monitor/verify.yml?branch=main&style=flat-square&label=verify" alt="Verification workflow" /></a>
    <a href="LICENSE" target="_blank" rel="noreferrer"><img src="https://img.shields.io/badge/License-PolyForm_Noncommercial_1.0.0-blue?style=flat-square" alt="PolyForm Noncommercial 1.0.0" /></a>
  </p>

  <p><a href="#preview">Preview</a> · <a href="#features">Features</a> · <a href="#getting-started">Getting Started</a> · <a href="#routes">Routes</a> · <a href="#language">Language</a> · <a href="docs/contract.md">Contract</a></p>
</div>

---

## Overview

`cliproxyapi-plugin-quota-monitor`는 CLIProxyAPI 호스트에 직접 로드되는 `c-shared` C ABI 기반 네이티브 Go 플러그인입니다. 등록된 AI 공급자 계정들의 잔여 한도, 리셋 주기, 활성/비활성 상태, 사용 가능한 크레딧 잔액을 주기적으로 안전하게 수집하고, 독립적인 임베디드 반응형 대시보드 및 관리 API를 제공합니다.

## Preview

| Desktop Dashboard | Mobile View |
| :---: | :---: |
| <img src="docs/assets/quota-desktop.ko.png" alt="계정 쿼터: 상태 요약, 계정별 한도와 초기화 시각" width="100%" /> | <img src="docs/assets/quota-mobile.ko.png" alt="모바일 계정 쿼터와 터치용 필터" width="280" /> |

## Features

- **계정별 쿼터 및 크레딧 추적**: Claude, Codex, Antigravity 등 주요 공급자 계정의 윈도우별 잔여 퍼센트, 리셋 예정 시각, 크레딧 잔액 모니터링.
- **최근 사용 계정 강조**: 프록시를 통해 최근 실제로 트래픽을 처리한 계정을 시각적으로 구분 표시.
- **제약적 수집기 (Bounded Collection)**: 계정별 조회 일정, 단일 백그라운드 수집 워커, 타임아웃 및 동시성 제어로 공급자 API 과부하 방지.
- **갱신 일관성 보호 (Refresh Consistency)**: 오래된 비동기 조회가 최신 갱신 데이터를 덮어쓰지 않도록 세대 기반 캐시 관리.
- **자립형 반응형 대시보드**: 외부 CDN이나 무거운 차트 라이브러리 없이 순수 HTML/CSS/JS 및 자체 호스팅 WOFF2 글꼴(SUIT, Manrope)로 렌더링.
- **한국어/영어 UI**: 대시보드 헤더에서 언어를 전환합니다([Language](#language) 참고).
- **안전한 엔드포인트 분리**: 읽기 전용 스냅샷 조회와 명시적 갱신 엔드포인트를 분리하고, 30초 전역 쿨다운 및 속도 제한 적용.

## Tech Stack

| Domain | Technology / Specification |
|---|---|
| Runtime & ABI | Go 1.26.5, cgo, C shared library (`.so`) |
| Host Integration | CLIProxyAPI Plugin C ABI (`v0` management/resource routes) |
| Embedded UI | HTML5, Vanilla JavaScript, CSS Grid/Flexbox |
| Bundled Typography | SUIT Regular/SemiBold, Manrope Variable (self-hosted WOFF2) |
| Verification | Go race/vet, Node 레이아웃·i18n 계약 테스트, Native C smoke test (`dlopen`) |

## Getting Started

호스트 프로세스와 동일한 OS·아키텍처·C 런타임 환경(Linux amd64 glibc)에서 빌드해야 합니다.

[mise](https://mise.jdx.dev)를 통해 로컬 런타임을 간편하게 부트스트랩할 수 있습니다:

```bash
# Managed runtimes
mise trust
mise install

# Ubuntu/Debian native build tools
sudo apt-get update && sudo apt-get install -y build-essential

# Run tests and native ABI smoke test
make verify
make native-smoke
```

### Build

```bash
make build
```

빌드 결과물은 `artifacts/quota-monitor.so` 및 헤더 파일 `artifacts/quota-monitor.h`로 생성됩니다.

## Language

대시보드는 한국어와 영어를 지원하며, 페이지 헤더의 `한국어 | English` 스위치로 전환합니다.

- **선택 순서**: 저장된 선택 → 브라우저 언어(한국어면 한국어, 그 외는 영어) → 한국어.
- **공유 설정**: 선택값은 브라우저의 `cpa_plugin_lang` 키에 저장되며, [usage-audit](https://github.com/dohwi/cliproxyapi-plugin-usage-audit) 대시보드와 같은 키를 씁니다. 한쪽에서 바꾸면 열려 있는 다른 쪽도 따라갑니다. 두 대시보드가 같은 origin에서 제공될 때만 적용됩니다.
- **개인정보**: 이 키가 페이지가 브라우저 저장소에 쓰는 유일한 값입니다.
- **번역되지 않는 항목**: 호스트 메뉴에 표시되는 플러그인 이름은 로드 시 한 번 등록되므로 한국어("계정 쿼터")로 유지됩니다.

## Routes

CLIProxyAPI 호스트 로드 후 아래 리소스 경로로 접근할 수 있습니다:

| Method | Route | Description |
|---|---|---|
| `GET` | `/v0/resource/plugins/quota-monitor/dashboard` | 계정 쿼터 반응형 웹 대시보드 |
| `GET` | `/v0/resource/plugins/quota-monitor/data/accounts` | 캐시된 계정 쿼터 스냅샷 (조회 유발 없음) |
| `GET` | `/v0/resource/plugins/quota-monitor/data/refresh?confirm=1` | 전체 계정 명시적 갱신 (30초 쿨다운 적용) |
| `POST` | `/v0/management/plugins/quota-monitor/refresh` | 관리자 인증 기반 강제 갱신 |
| `GET` | `/v0/management/plugins/quota-monitor/status` | 수집기 상태 및 통계 |

## Project Structure

```text
cliproxyapi-plugin-quota-monitor/
├── main.go                 # c-shared C ABI 엔트리포인트 (plugin.register, management routes)
├── internal/
│   ├── quota/              # 공급자별 쿼터 클라이언트, 파서 및 도메인 모델
│   ├── plugin/             # 플러그인 수명주기, 수집 스케줄러, 스냅샷, 핸들러
│   ├── authdir/            # CLIProxyAPI auth-dir 파서
│   ├── availability/       # 공급자 패밀리별 가용성 평가
│   ├── overview/           # 공급자별 보수적 쿼터 요약
│   └── dashboard/          # 내장 대시보드: dashboard.html, style.css, app.js, 폰트 에셋
├── docs/                   # 프로토콜 계약, 관리 명세 및 이미지
├── scripts/                # 대시보드 레이아웃 검증 스크립트
├── verification/           # dlopen 기반 네이티브 C ABI 스모크 테스트
├── Makefile                # 빌드, 검증, 스모크 테스트 타겟
└── THIRD_PARTY_NOTICES.md  # 번들 폰트 라이선스 고지
```

대시보드 소스는 `internal/dashboard/`의 일반 파일 3개이며, `go:embed`로 포함되어 시작 시 단일 자립형 페이지로 조립됩니다.

## Documentation

- [Plugin Contract](docs/contract.md): C ABI, 리소스 및 릴리스 계약
- [Management Contract](docs/management.md): 설정 및 관리 라우트 상세
- [Remaining API](docs/remaining-api.md): 스냅샷 및 갱신 API 명세
- [Collection Contract](docs/collection-contract.md): 백그라운드 수집 및 보안 정책

## License

[PolyForm Noncommercial 1.0.0](LICENSE) — 개인·연구·교육 등 비상업적 용도로 사용·수정·배포할 수 있으며, 상업적 이용은 허용되지 않습니다.

## Security & Attribution

- 키, OAuth 토큰, 실제 계정 자격증명은 저장소에 커밋하지 않습니다.
- 내장 대시보드 번들 폰트 출처 및 라이선스는 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)를 참고하세요.
