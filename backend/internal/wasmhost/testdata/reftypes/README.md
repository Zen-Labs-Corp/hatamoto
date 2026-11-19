Modules for `TestReferenceTypesAreOff`. Each `.wasm` is built from the `.wat`
next to it with `wat2wasm` (wabt 1.0.36, default features).

`overlong_index.wasm` has no `.wat`: it is `mvp.wasm` with the `call_indirect`
type and table immediates rewritten as five-byte padded LEB128 values
(`80 80 80 80 00`), the form `wasm-ld` leaves after relocation, with the code
section and function body sizes adjusted by hand.
