;; A second, externref table next to the usual funcref one.
(module
  (table 4 funcref)
  (table 1 externref)
  (func (export "run")))
