// `#[cfg(test)]` items do not exist in a normal build: rustc drops them
// (and their `#[test]` children) unless `--test` is passed, so a lowering
// that targets the normal build drops them too. The doc comment above the
// gate arrives as a `#[doc]` attribute — inert metadata must not hide the
// gate (it used to: requiring ALL attributes to be `cfg(test)` missed every
// documented test helper).
#[cfg(test)]
/// helper used only by unit tests
fn test_only_helper() -> i64 {
    41
}

fn real(x: i64) -> i64 {
    x + 1
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn helper_is_off_by_one() {
        assert_eq!(test_only_helper() + 1, real(41));
    }
}

fn main() {
    println!("{}", real(41));
}
