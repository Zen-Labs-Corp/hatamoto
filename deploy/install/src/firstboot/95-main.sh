# First-boot entry point. Carries no trust keys and downloads nothing.

main() {
  [ -z "${HATAMOTO_INSTALL_SOURCE_ONLY:-}" ] || return 0
  cmd="${1:-first-boot}"
  case "$cmd" in
    first-boot)
      need_root
      command -v stage_firstboot >/dev/null 2>&1 || die "stage not available in this build: stage_firstboot"
      stage_firstboot
      ;;
    set-domain)
      need_root
      shift
      fb_set_domain "$@"
      ;;
    version)
      echo "hatamoto-firstboot 1"
      ;;
    *)
      echo "usage: firstboot.sh [first-boot | set-domain HOST [--email ADDR] | version]" >&2
      exit 2
      ;;
  esac
}

main "$@"
