package dashboard

import _ "embed"

// SUIT is self-hosted from the SUIT project, SIL Open Font License 1.1.
// Provenance: copied from this workspace's verified SUIT distribution at V5.3.
// SHA-256 regular:  7a1971b4e54d6d797f105f2d8ad2fc5f5d6fd532195f582d74f4403177e83185
// SHA-256 semibold: a0ea79e549e2ef42930d16c9c5b40600132a7f3661fe0e26a84a20c2c8d182a0

// Manrope variable font is copied byte-for-byte from dohwi.com.
// SHA-256: a30ddcd349703aff7464c34bef3fffdff405ee50c113440d7c8693c02d210972
//
//go:embed assets/manrope-variable.woff2
var Manrope []byte

//go:embed assets/suit-regular.woff2
var SUITRegular []byte

//go:embed assets/suit-semibold.woff2
var SUITSemibold []byte
