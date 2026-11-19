;; MVP-style module: one funcref table, an active element segment for table 0,
;; call_indirect through table 0, bulk-memory copy/fill.
(module
  (type $t (func (result i32)))
  (memory 1)
  (table 4 funcref)
  (elem (i32.const 0) $f)
  (func $f (type $t) (i32.const 7))
  (func (export "run") (result i32)
    (memory.fill (i32.const 0) (i32.const 1) (i32.const 8))
    (memory.copy (i32.const 16) (i32.const 0) (i32.const 8))
    (call_indirect (type $t) (i32.const 0))))
