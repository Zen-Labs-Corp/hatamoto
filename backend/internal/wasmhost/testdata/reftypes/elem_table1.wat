;; Element segment targeting table 1 (the form that needs the table index).
(module
  (table 4 funcref)
  (table 4 funcref)
  (elem (table 1) (i32.const 0) func $f)
  (func $f)
  (func (export "run")))
