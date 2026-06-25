import { useEffect, useRef, useState } from "react";

// useShakeGravity wires up the "shake the Dogédex" easter egg: when the user
// intentionally shakes their phone, the cards that are currently on screen
// detach and fall under accelerometer-driven gravity, bouncing off the screen
// borders. The cards stay visually identical — we only ever set `transform`
// (and, while active, `position`/`z-index`) on them, never touching their
// backgrounds, borders, shadows, or content.
//
// All motion runs in a single requestAnimationFrame loop that mutates the DOM
// directly. Driving N cards through React state every frame would be far too
// heavy, and the card nodes carry no inline styles React would clobber, so
// direct mutation is both safe and fast.

// --- Tunables -------------------------------------------------------------

// Shake detection. We high-pass the acceleration signal and count direction
// reversals whose peak magnitude clears SHAKE_MAG within SHAKE_WINDOW_MS. A
// single jolt or drop produces one spike, not repeated reversals, so it cannot
// trigger by accident.
const SHAKE_MAG = 12; // m/s^2 peak to count a reversal
const SHAKE_REVERSALS = 4; // reversals needed to engage gravity
const SHAKE_WINDOW_MS = 700;

// Hint band: below the trigger but above the noise floor we apply a subtle
// wobble proportional to energy, so shaking is discoverable.
const HINT_FLOOR = 3; // m/s^2 where the wobble starts
const HINT_CEIL = SHAKE_MAG; // wobble maxes out at the trigger threshold
const HINT_MAX_PX = 3; // peak wobble translation
const HINT_MAX_DEG = 0.8; // peak wobble rotation

// Physics.
const GRAVITY_SCALE = 90; // device tilt (m/s^2) -> px/s^2
const BASE_GRAVITY = 2200; // fallback downward pull (px/s^2), keyboard trigger
const RESTITUTION = 0.6; // bounce energy retained at the borders
const AIR_DRAG = 0.0008; // per-frame velocity damping
const WALL_FRICTION = 0.82; // tangential damping on wall contact
const MAX_SPEED = 6000; // px/s clamp
const SLEEP_SPEED = 14; // px/s below which a resting card stops jittering
const MAX_ANGVEL = 220; // deg/s clamp
const BURST = 260; // initial outward velocity seeded from a shake

const KONAMI = [
  "ArrowUp",
  "ArrowUp",
  "ArrowDown",
  "ArrowDown",
  "ArrowLeft",
  "ArrowRight",
  "ArrowLeft",
  "ArrowRight",
  "b",
  "a",
];

interface Body {
  el: HTMLElement;
  x: number;
  y: number;
  w: number;
  h: number;
  vx: number;
  vy: number;
  angle: number;
  angVel: number;
  asleep: boolean;
}

interface DragState {
  body: Body;
  pointerId: number;
  // grab offset within the card
  dx: number;
  dy: number;
  // grab origin, to measure total travel for the tap/drag threshold
  originX: number;
  originY: number;
  // recent samples for release velocity
  lastX: number;
  lastY: number;
  lastT: number;
  vx: number;
  vy: number;
  moved: boolean;
}

// Pointer travel (px) past which a press is treated as a drag, not a tap.
const DRAG_THRESHOLD = 6;

function prefersReducedMotion(): boolean {
  return (
    typeof matchMedia === "function" &&
    matchMedia("(prefers-reduced-motion: reduce)").matches
  );
}

// iOS 13+ gates DeviceMotionEvent behind a permission call that must run inside
// a user gesture. We request it once, silently, on the first tap.
type MotionCtor = typeof DeviceMotionEvent & {
  requestPermission?: () => Promise<"granted" | "denied">;
};

export interface ShakeGravity {
  active: boolean;
  deactivate: () => void;
}

export function useShakeGravity(
  containerRef: React.RefObject<HTMLElement | null>,
): ShakeGravity {
  const [active, setActive] = useState(false);
  // Imperative state lives in refs so the rAF loop and listeners are stable and
  // never trigger re-renders.
  const bodiesRef = useRef<Body[]>([]);
  const rafRef = useRef<number | null>(null);
  const activeRef = useRef(false);
  // Gravity direction in screen space (unit-ish vector from device tilt).
  const gravRef = useRef({ x: 0, y: 1 });
  const tiltRef = useRef(false); // true once we have real accelerometer data
  const hintRef = useRef(0); // smoothed hint energy 0..1
  const deactivateRef = useRef<() => void>(() => {});

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;

    const reduced = prefersReducedMotion();

    // --- Shake detection state ---------------------------------------------
    // Low-pass gravity estimate so we can high-pass the raw signal when only
    // accelerationIncludingGravity is available.
    const lp = { x: 0, y: 0, z: 0 };
    let lpInit = false;
    let lastSign = 0;
    const reversals: number[] = []; // timestamps within the sliding window

    // --- Body helpers ------------------------------------------------------

    function visibleCards(): HTMLElement[] {
      const vw = window.innerWidth;
      const vh = window.innerHeight;
      return Array.from(
        container!.querySelectorAll<HTMLElement>(".card"),
      ).filter((el) => {
        const r = el.getBoundingClientRect();
        return (
          r.bottom > 0 && r.top < vh && r.right > 0 && r.left < vw &&
          r.width > 0 && r.height > 0
        );
      });
    }

    function setTransform(b: Body) {
      b.el.style.transform = `translate(${b.x}px, ${b.y}px) rotate(${b.angle}deg)`;
    }

    // --- Activation / teardown --------------------------------------------

    // Frozen layout bookkeeping so detaching cards doesn't reflow the page.
    let scrollLockY = 0;
    const frozen: Array<{ el: HTMLElement; height: string }> = [];

    // Shake auto-trigger is suppressed under prefers-reduced-motion (guarded in
    // onMotion); the explicit keyboard trigger may still opt in.
    function activate(seedFromShake: boolean) {
      if (activeRef.current) return;
      const cards = visibleCards();
      if (cards.length === 0) return;

      // Freeze every .cards container's height so removing its children from
      // flow leaves the page geometry unchanged.
      const containers = new Set<HTMLElement>();
      cards.forEach((c) => {
        const grid = c.closest<HTMLElement>(".cards");
        if (grid) containers.add(grid);
      });
      containers.forEach((el) => {
        frozen.push({ el, height: el.style.height });
        el.style.height = `${el.getBoundingClientRect().height}px`;
      });

      // Lock scroll at the current position; cards fly in fixed coordinates.
      scrollLockY = window.scrollY;

      const bodies: Body[] = cards.map((el) => {
        const r = el.getBoundingClientRect();
        const b: Body = {
          el,
          x: r.left,
          y: r.top,
          w: r.width,
          h: r.height,
          vx: 0,
          vy: 0,
          angle: 0,
          angVel: 0,
          asleep: false,
        };
        // Pin in place first — no visual jump at the moment of detach.
        el.style.position = "fixed";
        el.style.left = "0";
        el.style.top = "0";
        el.style.width = `${r.width}px`;
        el.style.height = `${r.height}px`;
        el.style.margin = "0";
        el.style.zIndex = "900";
        el.style.willChange = "transform";
        el.style.touchAction = "none";
        setTransform(b);
        if (seedFromShake) {
          b.vx = (Math.random() - 0.5) * BURST;
          b.vy = -Math.random() * BURST;
          b.angVel = (Math.random() - 0.5) * MAX_ANGVEL;
        }
        return b;
      });
      bodiesRef.current = bodies;

      document.body.style.overflow = "hidden";
      window.scrollTo(0, scrollLockY);

      cards.forEach((el) => {
        el.addEventListener("pointerdown", onPointerDown);
      });

      activeRef.current = true;
      setActive(true);
      lastFrame = 0;
      rafRef.current = requestAnimationFrame(step);
    }

    function deactivate() {
      if (!activeRef.current) return;
      activeRef.current = false;
      setActive(false);
      if (rafRef.current != null) cancelAnimationFrame(rafRef.current);
      rafRef.current = null;
      bodiesRef.current.forEach((b) => {
        b.el.removeEventListener("pointerdown", onPointerDown);
        const s = b.el.style;
        s.position = "";
        s.left = "";
        s.top = "";
        s.width = "";
        s.height = "";
        s.margin = "";
        s.zIndex = "";
        s.willChange = "";
        s.touchAction = "";
        s.transform = "";
      });
      bodiesRef.current = [];
      frozen.forEach(({ el, height }) => (el.style.height = height));
      frozen.length = 0;
      document.body.style.overflow = "";
    }
    deactivateRef.current = deactivate;

    // --- Pointer drag (fling) ----------------------------------------------

    let drag: DragState | null = null;

    function onPointerDown(e: PointerEvent) {
      const el = e.currentTarget as HTMLElement;
      const b = bodiesRef.current.find((x) => x.el === el);
      if (!b) return;
      b.asleep = false;
      drag = {
        body: b,
        pointerId: e.pointerId,
        dx: e.clientX - b.x,
        dy: e.clientY - b.y,
        originX: e.clientX,
        originY: e.clientY,
        lastX: e.clientX,
        lastY: e.clientY,
        lastT: performance.now(),
        vx: 0,
        vy: 0,
        moved: false,
      };
      el.setPointerCapture(e.pointerId);
      window.addEventListener("pointermove", onPointerMove);
      window.addEventListener("pointerup", onPointerUp);
      window.addEventListener("pointercancel", onPointerUp);
    }

    function onPointerMove(e: PointerEvent) {
      if (!drag || e.pointerId !== drag.pointerId) return;
      const b = drag.body;
      b.x = e.clientX - drag.dx;
      b.y = e.clientY - drag.dy;
      const now = performance.now();
      const dt = Math.max(1, now - drag.lastT) / 1000;
      drag.vx = (e.clientX - drag.lastX) / dt;
      drag.vy = (e.clientY - drag.lastY) / dt;
      drag.lastX = e.clientX;
      drag.lastY = e.clientY;
      drag.lastT = now;
      if (
        Math.hypot(e.clientX - drag.originX, e.clientY - drag.originY) >
        DRAG_THRESHOLD
      )
        drag.moved = true; // clearly a drag -> suppress click-to-open on release
      setTransform(b);
    }

    function onPointerUp(e: PointerEvent) {
      if (!drag || e.pointerId !== drag.pointerId) return;
      const b = drag.body;
      b.vx = drag.vx;
      b.vy = drag.vy;
      b.asleep = false;
      // If it was effectively a tap (no real movement), let the click through
      // by not capturing it; otherwise swallow the upcoming click.
      if (drag.moved) {
        const swallow = (ev: Event) => {
          ev.stopPropagation();
          ev.preventDefault();
          b.el.removeEventListener("click", swallow, true);
        };
        b.el.addEventListener("click", swallow, true);
        setTimeout(() => b.el.removeEventListener("click", swallow, true), 0);
      }
      drag = null;
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", onPointerUp);
      window.removeEventListener("pointercancel", onPointerUp);
    }

    // --- Physics loop ------------------------------------------------------

    let lastFrame = 0;

    function step(t: number) {
      if (!activeRef.current) return;
      if (!lastFrame) lastFrame = t;
      let dt = (t - lastFrame) / 1000;
      lastFrame = t;
      if (dt > 0.05) dt = 0.05; // clamp after tab-switch / long frames

      const vw = window.innerWidth;
      const vh = window.innerHeight;
      const g = gravRef.current;
      const gx = g.x * (tiltRef.current ? GRAVITY_SCALE : BASE_GRAVITY);
      const gy = g.y * (tiltRef.current ? GRAVITY_SCALE : BASE_GRAVITY);

      for (const b of bodiesRef.current) {
        if (drag && drag.body === b) continue; // user owns it this frame
        if (b.asleep) continue;

        b.vx += gx * dt;
        b.vy += gy * dt;
        // air drag
        const damp = 1 - AIR_DRAG * 1000 * dt;
        b.vx *= damp;
        b.vy *= damp;
        // clamp
        const sp = Math.hypot(b.vx, b.vy);
        if (sp > MAX_SPEED) {
          b.vx = (b.vx / sp) * MAX_SPEED;
          b.vy = (b.vy / sp) * MAX_SPEED;
        }

        b.x += b.vx * dt;
        b.y += b.vy * dt;
        b.angle += b.angVel * dt;
        b.angVel *= 0.99;
        if (Math.abs(b.angVel) > MAX_ANGVEL)
          b.angVel = Math.sign(b.angVel) * MAX_ANGVEL;

        const maxX = Math.max(0, vw - b.w);
        const maxY = Math.max(0, vh - b.h);
        // Bounce on the viewport borders.
        if (b.x < 0) {
          b.x = 0;
          b.vx = -b.vx * RESTITUTION;
          b.vy *= WALL_FRICTION;
          b.angVel += b.vy * 0.01;
        } else if (b.x > maxX) {
          b.x = maxX;
          b.vx = -b.vx * RESTITUTION;
          b.vy *= WALL_FRICTION;
          b.angVel -= b.vy * 0.01;
        }
        if (b.y < 0) {
          b.y = 0;
          b.vy = -b.vy * RESTITUTION;
          b.vx *= WALL_FRICTION;
        } else if (b.y > maxY) {
          b.y = maxY;
          b.vy = -b.vy * RESTITUTION;
          b.vx *= WALL_FRICTION;
          b.angVel *= 0.9;
        }

        // Settle: when resting on the gravity-low edge and slow, sleep to stop
        // sub-pixel jitter.
        const resting =
          (gy > 0 && b.y >= maxY - 0.5) ||
          (gy < 0 && b.y <= 0.5) ||
          (gx > 0 && b.x >= maxX - 0.5) ||
          (gx < 0 && b.x <= 0.5);
        if (resting && Math.hypot(b.vx, b.vy) < SLEEP_SPEED) {
          b.vx = 0;
          b.vy = 0;
          b.angVel *= 0.8;
          if (Math.abs(b.angVel) < 2) {
            b.angVel = 0;
            b.asleep = true;
          }
        }

        setTransform(b);
      }

      rafRef.current = requestAnimationFrame(step);
    }

    // --- Hint loop (only while idle) ---------------------------------------

    let hintRaf: number | null = null;
    function hintStep() {
      if (activeRef.current) {
        hintRaf = null;
        return;
      }
      const energy = hintRef.current; // 0..1
      const cards = container!.querySelectorAll<HTMLElement>(".card");
      if (energy < 0.001) {
        cards.forEach((el) => {
          if (el.style.transform) el.style.transform = "";
        });
        hintRaf = null;
        return;
      }
      const tx = Math.sin(performance.now() / 45) * HINT_MAX_PX * energy;
      const ty = Math.cos(performance.now() / 38) * HINT_MAX_PX * energy;
      const rot = Math.sin(performance.now() / 52) * HINT_MAX_DEG * energy;
      cards.forEach((el) => {
        el.style.transform = `translate(${tx}px, ${ty}px) rotate(${rot}deg)`;
      });
      hintRef.current *= 0.9; // decay; fresh shakes top it back up
      hintRaf = requestAnimationFrame(hintStep);
    }
    function pokeHint(level: number) {
      if (reduced) return;
      hintRef.current = Math.max(hintRef.current, level);
      if (hintRaf == null && !activeRef.current)
        hintRaf = requestAnimationFrame(hintStep);
    }

    // --- Device motion -----------------------------------------------------

    function onMotion(e: DeviceMotionEvent) {
      const acc = e.acceleration;
      const accG = e.accelerationIncludingGravity;
      let ax = 0;
      let ay = 0;
      let az = 0;
      if (acc && acc.x != null) {
        ax = acc.x ?? 0;
        ay = acc.y ?? 0;
        az = acc.z ?? 0;
        if (accG) {
          // Tilt direction from the gravity component (screen axes: device +x
          // is right, +y is up, so screen-down is -y).
          gravRef.current = normGravity(-(accG.x ?? 0), accG.y ?? 0);
          tiltRef.current = true;
        }
      } else if (accG) {
        // High-pass to recover linear acceleration; the low-pass is gravity.
        const k = lpInit ? 0.8 : 0;
        lp.x = k * lp.x + (1 - k) * (accG.x ?? 0);
        lp.y = k * lp.y + (1 - k) * (accG.y ?? 0);
        lp.z = k * lp.z + (1 - k) * (accG.z ?? 0);
        lpInit = true;
        ax = (accG.x ?? 0) - lp.x;
        ay = (accG.y ?? 0) - lp.y;
        az = (accG.z ?? 0) - lp.z;
        gravRef.current = normGravity(-lp.x, lp.y);
        tiltRef.current = true;
      }

      const mag = Math.hypot(ax, ay, az);

      // Hint band: subtle wobble as the user starts to move.
      if (!activeRef.current && mag > HINT_FLOOR) {
        const level = Math.min(1, (mag - HINT_FLOOR) / (HINT_CEIL - HINT_FLOOR));
        pokeHint(level);
      }

      // Intentional-shake detection via direction reversals.
      const now = performance.now();
      const sign = ax + ay > 0 ? 1 : -1;
      if (mag > SHAKE_MAG && sign !== lastSign) {
        lastSign = sign;
        reversals.push(now);
        while (reversals.length && now - reversals[0] > SHAKE_WINDOW_MS)
          reversals.shift();
        if (reversals.length >= SHAKE_REVERSALS && !activeRef.current && !reduced) {
          reversals.length = 0;
          hintRef.current = 0;
          activate(true);
        }
      }
    }

    function normGravity(x: number, y: number) {
      const m = Math.hypot(x, y);
      if (m < 0.5) return { x: 0, y: 1 }; // flat / no clear tilt -> straight down
      return { x: x / m, y: y / m };
    }

    let motionBound = false;
    function bindMotion() {
      if (motionBound) return;
      motionBound = true;
      window.addEventListener("devicemotion", onMotion);
    }

    // iOS: request permission once on the first tap, then bind.
    const MotionEvent = window.DeviceMotionEvent as MotionCtor | undefined;
    function onFirstTap() {
      document.removeEventListener("pointerdown", onFirstTap);
      if (MotionEvent && typeof MotionEvent.requestPermission === "function") {
        MotionEvent.requestPermission()
          .then((res) => {
            if (res === "granted") bindMotion();
          })
          .catch(() => {});
      }
    }
    if (MotionEvent) {
      if (typeof MotionEvent.requestPermission === "function") {
        // iOS — defer to first gesture.
        document.addEventListener("pointerdown", onFirstTap);
      } else {
        // Android / others — bind immediately.
        bindMotion();
      }
    }

    // --- Konami (desktop trigger) ------------------------------------------

    let kIdx = 0;
    function onKey(e: KeyboardEvent) {
      const key = e.key.length === 1 ? e.key.toLowerCase() : e.key;
      kIdx = key === KONAMI[kIdx] ? kIdx + 1 : key === KONAMI[0] ? 1 : 0;
      if (kIdx === KONAMI.length) {
        kIdx = 0;
        if (activeRef.current) deactivate();
        else {
          gravRef.current = { x: (Math.random() - 0.5) * 0.2, y: 1 };
          tiltRef.current = false;
          activate(true);
        }
      }
      // Nudge gravity direction with arrows while active (desktop fun).
      if (activeRef.current) {
        if (e.key === "ArrowLeft") gravRef.current = { x: -1, y: 0 };
        else if (e.key === "ArrowRight") gravRef.current = { x: 1, y: 0 };
        else if (e.key === "ArrowUp") gravRef.current = { x: 0, y: -1 };
        else if (e.key === "ArrowDown") gravRef.current = { x: 0, y: 1 };
        if (e.key.startsWith("Arrow"))
          bodiesRef.current.forEach((b) => (b.asleep = false));
      }
      if (e.key === "Escape" && activeRef.current) deactivate();
    }
    window.addEventListener("keydown", onKey);

    // --- Cleanup -----------------------------------------------------------

    return () => {
      deactivate();
      window.removeEventListener("devicemotion", onMotion);
      document.removeEventListener("pointerdown", onFirstTap);
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", onPointerUp);
      window.removeEventListener("pointercancel", onPointerUp);
      if (hintRaf != null) cancelAnimationFrame(hintRaf);
    };
  }, [containerRef]);

  return {
    active,
    deactivate: () => deactivateRef.current(),
  };
}
