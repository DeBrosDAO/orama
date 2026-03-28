import { useRef, useMemo } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import { Float } from "@react-three/drei";
import * as THREE from "three";

const NODE_COUNT = 24;
const INNER_RADIUS = 0.6;
const OUTER_RADIUS = 1.6;
const PARTICLE_COUNT = 80;

/* ─── Dot ring (reusable) ─── */
function DotRing({ radius, count, color, opacity, dotSize }: {
  radius: number; count: number; color: string; opacity: number; dotSize: number;
}) {
  const dots = useMemo(() =>
    Array.from({ length: count }, (_, i) => {
      const angle = (i / count) * Math.PI * 2;
      return [Math.cos(angle) * radius, Math.sin(angle) * radius] as [number, number];
    }), [radius, count]);

  return (
    <group rotation={[-Math.PI / 2, 0, 0]}>
      {dots.map(([x, y], i) => (
        <mesh key={i} position={[x, y, 0]}>
          <circleGeometry args={[dotSize, 8]} />
          <meshBasicMaterial color={color} transparent opacity={opacity} side={THREE.DoubleSide} />
        </mesh>
      ))}
    </group>
  );
}

/* ─── Core nodes — pulsing spheres in a scattered cluster ─── */
function CoreNodes() {
  const meshRefs = useRef<(THREE.Mesh | null)[]>([]);

  const nodes = useMemo(() =>
    Array.from({ length: NODE_COUNT }, (_, i) => {
      const angle = (i / NODE_COUNT) * Math.PI * 2 + (Math.random() - 0.5) * 0.3;
      const r = INNER_RADIUS + Math.random() * (OUTER_RADIUS - INNER_RADIUS);
      const y = (Math.random() - 0.5) * 0.6;
      return {
        x: Math.cos(angle) * r,
        y,
        z: Math.sin(angle) * r,
        phase: i * 0.4 + Math.random() * 2,
        size: 0.015 + Math.random() * 0.02,
      };
    }), []);

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    meshRefs.current.forEach((mesh, i) => {
      if (!mesh) return;
      const node = nodes[i];
      const wave = Math.sin(t * 1.2 + node.phase);
      const mat = mesh.material as THREE.MeshBasicMaterial;
      mat.opacity = 0.2 + 0.5 * Math.max(0, wave);
      const s = 1 + 0.4 * Math.max(0, wave);
      mesh.scale.setScalar(s);
    });
  });

  return (
    <group>
      {nodes.map((node, i) => (
        <mesh
          key={i}
          ref={(el) => { meshRefs.current[i] = el; }}
          position={[node.x, node.y, node.z]}
        >
          <sphereGeometry args={[node.size, 8, 8]} />
          <meshBasicMaterial color="#d4d4d8" transparent opacity={0.3} />
        </mesh>
      ))}
    </group>
  );
}

/* ─── Connection lines between nearby nodes ─── */
function Connections() {
  const lineRefs = useRef<(THREE.Line | null)[]>([]);

  const { nodes, pairs } = useMemo(() => {
    const ns = Array.from({ length: NODE_COUNT }, (_, i) => {
      const angle = (i / NODE_COUNT) * Math.PI * 2 + (Math.random() - 0.5) * 0.3;
      const r = INNER_RADIUS + Math.random() * (OUTER_RADIUS - INNER_RADIUS);
      const y = (Math.random() - 0.5) * 0.6;
      return new THREE.Vector3(Math.cos(angle) * r, y, Math.sin(angle) * r);
    });
    const ps: [number, number][] = [];
    for (let i = 0; i < ns.length; i++) {
      for (let j = i + 1; j < ns.length; j++) {
        if (ns[i].distanceTo(ns[j]) < 0.9) {
          ps.push([i, j]);
        }
      }
    }
    return { nodes: ns, pairs: ps };
  }, []);

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    lineRefs.current.forEach((line, idx) => {
      if (!line) return;
      const mat = line.material as THREE.LineBasicMaterial;
      const wave = Math.sin(t * 0.8 + idx * 0.3);
      mat.opacity = 0.03 + 0.06 * Math.max(0, wave);
    });
  });

  return (
    <group>
      {pairs.map(([a, b], i) => {
        const geo = new THREE.BufferGeometry().setFromPoints([nodes[a], nodes[b]]);
        return (
          <primitive
            key={i}
            ref={(el: THREE.Line | null) => { lineRefs.current[i] = el; }}
            object={new THREE.Line(geo, new THREE.LineBasicMaterial({ color: "#a1a1aa", transparent: true, opacity: 0.05 }))}
          />
        );
      })}
    </group>
  );
}

/* ─── Expanding pulse rings from center ─── */
function PulseRings() {
  const meshRefs = useRef<(THREE.Mesh | null)[]>([]);
  const RING_COUNT = 3;

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    meshRefs.current.forEach((mesh, i) => {
      if (!mesh) return;
      const phase = (t * 0.3 + i * (1 / RING_COUNT)) % 1;
      const scale = 0.2 + phase * 2;
      mesh.scale.set(scale, scale, 1);
      const mat = mesh.material as THREE.MeshBasicMaterial;
      mat.opacity = 0.08 * (1 - phase);
    });
  });

  return (
    <group rotation={[-Math.PI / 2, 0, 0]}>
      {Array.from({ length: RING_COUNT }, (_, i) => (
        <mesh key={i} ref={(el) => { meshRefs.current[i] = el; }}>
          <ringGeometry args={[0.98, 1, 64]} />
          <meshBasicMaterial color="#d4d4d8" transparent opacity={0.05} side={THREE.DoubleSide} />
        </mesh>
      ))}
    </group>
  );
}

/* ─── Floating ambient particles ─── */
function AmbientParticles() {
  const ref = useRef<THREE.Points>(null);

  const positions = useMemo(() => {
    const arr = new Float32Array(PARTICLE_COUNT * 3);
    for (let i = 0; i < PARTICLE_COUNT; i++) {
      const angle = Math.random() * Math.PI * 2;
      const r = 0.3 + Math.random() * 2.2;
      arr[i * 3] = Math.cos(angle) * r;
      arr[i * 3 + 1] = (Math.random() - 0.5) * 1.5;
      arr[i * 3 + 2] = Math.sin(angle) * r;
    }
    return arr;
  }, []);

  useFrame(({ clock }) => {
    if (!ref.current) return;
    const arr = ref.current.geometry.attributes.position.array as Float32Array;
    const t = clock.getElapsedTime();
    for (let i = 0; i < PARTICLE_COUNT; i++) {
      arr[i * 3 + 1] += Math.sin(t * 0.5 + i) * 0.0003;
    }
    ref.current.geometry.attributes.position.needsUpdate = true;
    ref.current.rotation.y = t * 0.02;
  });

  return (
    <points ref={ref}>
      <bufferGeometry>
        <bufferAttribute
          attach="attributes-position"
          args={[positions, 3]}
        />
      </bufferGeometry>
      <pointsMaterial color="#a1a1aa" size={0.008} transparent opacity={0.15} sizeAttenuation />
    </points>
  );
}

/* ─── Center shield icon (icosahedron wireframe) ─── */
function CenterShield() {
  const meshRef = useRef<THREE.Mesh>(null);
  const glowRef = useRef<THREE.Mesh>(null);

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    if (meshRef.current) {
      meshRef.current.rotation.y = t * 0.15;
      meshRef.current.rotation.x = Math.sin(t * 0.1) * 0.1;
    }
    if (glowRef.current) {
      const mat = glowRef.current.material as THREE.MeshBasicMaterial;
      mat.opacity = 0.03 + 0.02 * Math.sin(t * 1.5);
      const s = 1 + 0.05 * Math.sin(t * 1.5);
      glowRef.current.scale.setScalar(s);
    }
  });

  return (
    <group>
      <mesh ref={meshRef}>
        <icosahedronGeometry args={[0.18, 1]} />
        <meshBasicMaterial color="#d4d4d8" wireframe transparent opacity={0.25} />
      </mesh>
      <mesh ref={glowRef}>
        <sphereGeometry args={[0.22, 16, 16]} />
        <meshBasicMaterial color="#d4d4d8" transparent opacity={0.03} />
      </mesh>
    </group>
  );
}

/* ─── Full scene ─── */
function AboutNetwork() {
  const groupRef = useRef<THREE.Group>(null);

  useFrame(({ clock }) => {
    if (groupRef.current) {
      groupRef.current.rotation.y = clock.getElapsedTime() * 0.03;
    }
  });

  return (
    <Float speed={0.5} rotationIntensity={0.01} floatIntensity={0.04}>
      <group ref={groupRef}>
        <DotRing radius={OUTER_RADIUS} count={80} color="#a1a1aa" opacity={0.03} dotSize={0.004} />
        <DotRing radius={INNER_RADIUS} count={40} color="#d4d4d8" opacity={0.04} dotSize={0.003} />
        <CoreNodes />
        <Connections />
        <PulseRings />
        <AmbientParticles />
        <CenterShield />
      </group>
    </Float>
  );
}

export function AboutHeroScene() {
  return (
    <div className="w-full h-[350px] md:h-[550px] -mt-[250px] md:-mt-[400px]">
      <Canvas
        camera={{ position: [0, 2, 2.5], fov: 42 }}
        dpr={[1, 2]}
        gl={{ antialias: true, alpha: true, toneMapping: THREE.ACESFilmicToneMapping, toneMappingExposure: 1 }}
        style={{ background: "transparent" }}
      >
        <ambientLight intensity={0.1} />
        <AboutNetwork />
      </Canvas>
    </div>
  );
}
