import { useRef, useMemo } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import { Float } from "@react-three/drei";
import * as THREE from "three";

const VALIDATOR_COUNT = 12;
const ANGEL_COUNT = 8;
const RING_RADIUS = 1.4;
const ANGEL_RADIUS = 0.9;

/* ─── Thin ring made of dots ─── */
function DotRing({ radius, count, color, opacity, dotSize }: {
  radius: number; count: number; color: string; opacity: number; dotSize: number;
}) {
  const dots = useMemo(() => {
    return Array.from({ length: count }, (_, i) => {
      const angle = (i / count) * Math.PI * 2;
      return [Math.cos(angle) * radius, Math.sin(angle) * radius] as [number, number];
    });
  }, [radius, count]);

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

/* ─── Validator nodes — small glowing dots on the outer ring ─── */
function Validators() {
  const groupRef = useRef<THREE.Group>(null);
  const meshRefs = useRef<(THREE.Mesh | null)[]>([]);

  const positions = useMemo(() => {
    return Array.from({ length: VALIDATOR_COUNT }, (_, i) => {
      const angle = (i / VALIDATOR_COUNT) * Math.PI * 2;
      return {
        x: Math.cos(angle) * RING_RADIUS,
        z: Math.sin(angle) * RING_RADIUS,
        phase: i * 0.5,
      };
    });
  }, []);

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    meshRefs.current.forEach((mesh, i) => {
      if (!mesh) return;
      const mat = mesh.material as THREE.MeshBasicMaterial;
      // Staggered pulse wave around the ring
      const wave = Math.sin(t * 1.5 - positions[i].phase);
      mat.opacity = 0.3 + 0.4 * Math.max(0, wave);
      const s = 1 + 0.3 * Math.max(0, wave);
      mesh.scale.setScalar(s);
    });
  });

  return (
    <group ref={groupRef}>
      {positions.map((pos, i) => (
        <mesh
          key={i}
          ref={(el) => { meshRefs.current[i] = el; }}
          position={[pos.x, 0, pos.z]}
        >
          <sphereGeometry args={[0.03, 12, 12]} />
          <meshBasicMaterial color="#d4d4d8" transparent opacity={0.4} />
        </mesh>
      ))}
    </group>
  );
}

/* ─── Angel nodes — teal particles orbiting inner ring ─── */
function Angels() {
  const groupRef = useRef<THREE.Group>(null);
  const meshRefs = useRef<(THREE.Mesh | null)[]>([]);
  const glowRefs = useRef<(THREE.Mesh | null)[]>([]);

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();

    meshRefs.current.forEach((mesh, i) => {
      if (!mesh) return;
      const angle = (i / ANGEL_COUNT) * Math.PI * 2 + t * 0.4;
      const wobbleY = Math.sin(t * 2.5 + i * 1.2) * 0.08;

      mesh.position.x = Math.cos(angle) * ANGEL_RADIUS;
      mesh.position.z = Math.sin(angle) * ANGEL_RADIUS;
      mesh.position.y = wobbleY;

      const mat = mesh.material as THREE.MeshBasicMaterial;
      const pulse = Math.sin(t * 2 + i * 0.8);
      mat.opacity = 0.5 + 0.3 * Math.max(0, pulse);
    });

    glowRefs.current.forEach((mesh, i) => {
      if (!mesh) return;
      const angle = (i / ANGEL_COUNT) * Math.PI * 2 + t * 0.4;
      const wobbleY = Math.sin(t * 2.5 + i * 1.2) * 0.08;

      mesh.position.x = Math.cos(angle) * ANGEL_RADIUS;
      mesh.position.z = Math.sin(angle) * ANGEL_RADIUS;
      mesh.position.y = wobbleY;

      const mat = mesh.material as THREE.MeshBasicMaterial;
      const pulse = Math.sin(t * 2 + i * 0.8);
      mat.opacity = 0.06 + 0.08 * Math.max(0, pulse);
    });
  });

  return (
    <group ref={groupRef}>
      {Array.from({ length: ANGEL_COUNT }, (_, i) => (
        <group key={i}>
          <mesh ref={(el) => { meshRefs.current[i] = el; }}>
            <sphereGeometry args={[0.02, 8, 8]} />
            <meshBasicMaterial color="#00d4aa" transparent opacity={0.5} />
          </mesh>
          <mesh ref={(el) => { glowRefs.current[i] = el; }}>
            <sphereGeometry args={[0.06, 8, 8]} />
            <meshBasicMaterial color="#00d4aa" transparent opacity={0.06} />
          </mesh>
        </group>
      ))}
    </group>
  );
}

/* ─── Center block — small, elegant, rotating ─── */
function CenterBlock() {
  const meshRef = useRef<THREE.Mesh>(null);
  const pulseRef = useRef<THREE.Mesh>(null);

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();

    if (meshRef.current) {
      meshRef.current.rotation.y = t * 0.6;
      meshRef.current.rotation.z = t * 0.3;
    }

    // Pulse ring expands outward periodically
    if (pulseRef.current) {
      const cycle = t % 3;
      const expanding = cycle < 1;
      const scale = expanding ? 1 + cycle * 2 : 3;
      pulseRef.current.scale.setScalar(scale);
      const mat = pulseRef.current.material as THREE.MeshBasicMaterial;
      mat.opacity = expanding ? 0.08 * (1 - cycle) : 0;
    }
  });

  return (
    <group>
      <mesh ref={meshRef}>
        <octahedronGeometry args={[0.1, 0]} />
        <meshBasicMaterial color="#d4d4d8" transparent opacity={0.6} wireframe />
      </mesh>
      {/* Inner solid */}
      <mesh rotation={[0, 0.5, 0.5]}>
        <octahedronGeometry args={[0.05, 0]} />
        <meshBasicMaterial color="#d4d4d8" transparent opacity={0.3} />
      </mesh>
      {/* Expanding pulse ring */}
      <mesh ref={pulseRef} rotation={[-Math.PI / 2, 0, 0]}>
        <ringGeometry args={[0.12, 0.13, 32]} />
        <meshBasicMaterial color="#d4d4d8" transparent opacity={0} side={THREE.DoubleSide} />
      </mesh>
    </group>
  );
}

/* ─── Connection lines from validators to center ─── */
function ConnectionLines() {
  const lines = useMemo(() => {
    return Array.from({ length: VALIDATOR_COUNT }, (_, i) => {
      const angle = (i / VALIDATOR_COUNT) * Math.PI * 2;
      const x = Math.cos(angle) * RING_RADIUS;
      const z = Math.sin(angle) * RING_RADIUS;
      const geo = new THREE.BufferGeometry().setFromPoints([
        new THREE.Vector3(0, 0, 0),
        new THREE.Vector3(x, 0, z),
      ]);
      const mat = new THREE.LineBasicMaterial({ color: "#a1a1aa", transparent: true, opacity: 0.04 });
      return new THREE.Line(geo, mat);
    });
  }, []);

  return (
    <group>
      {lines.map((line, i) => (
        <primitive key={i} object={line} />
      ))}
    </group>
  );
}

/* ─── Full Scene ─── */
function ConsensusNetwork() {
  const groupRef = useRef<THREE.Group>(null);

  useFrame(({ clock }) => {
    if (groupRef.current) {
      groupRef.current.rotation.y = clock.getElapsedTime() * 0.05;
    }
  });

  return (
    <Float speed={0.6} rotationIntensity={0.01} floatIntensity={0.05}>
      <group ref={groupRef}>
        {/* Outer validator ring (dots) */}
        <DotRing radius={RING_RADIUS} count={60} color="#a1a1aa" opacity={0.04} dotSize={0.005} />
        {/* Inner angel ring (dots) */}
        <DotRing radius={ANGEL_RADIUS} count={40} color="#00d4aa" opacity={0.03} dotSize={0.004} />

        <ConnectionLines />
        <Validators />
        <Angels />
        <CenterBlock />
      </group>
    </Float>
  );
}

export function ConsensusScene() {
  return (
    <div className="w-full h-[350px] md:h-[550px] -mt-[250px] md:-mt-[400px]">
      <Canvas
        camera={{ position: [0, 2, 2], fov: 45 }}
        dpr={[1, 2]}
        gl={{ antialias: true, alpha: true, toneMapping: THREE.ACESFilmicToneMapping, toneMappingExposure: 1 }}
        style={{ background: "transparent" }}
      >
        <ambientLight intensity={0.1} />
        <ConsensusNetwork />
      </Canvas>
    </div>
  );
}
