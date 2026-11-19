;; Uses reference-type instructions on table 0: ref.func, table.get, table.set.
(module
  (table 4 funcref)
  (elem declare func $f)
  (func $f)
  (func (export "run")
    (table.set 0 (i32.const 1) (ref.func $f))
    (drop (table.get 0 (i32.const 1)))))
