import logoLight from "../assets/logo-light.svg";
import logoDark from "../assets/logo-dark.svg";

// Light/dark wordmark logos, matching the docs site. CSS swaps which one is
// shown based on the active color scheme.
export function Logo() {
  return (
    <>
      <img className="logo logo-light" src={logoLight} alt="gotifacts" />
      <img className="logo logo-dark" src={logoDark} alt="gotifacts" />
    </>
  );
}
