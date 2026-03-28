import { useRef, useMemo } from "react";
import { Canvas, useFrame } from "@react-three/fiber";
import { Float } from "@react-three/drei";
import * as THREE from "three";

const PARTICLE_COUNT = 60;
const ORAMA_RING_RADIUS = 1.6;
const PROXY_RING_RADIUS = 1.2;

/* ─── Wireframe vault (dodecahedron) ─── */
function Vault() {
  const meshRef = useRef<THREE.Mesh>(null);
  const innerRef = useRef<THREE.Mesh>(null);
  const pulseRef = useRef<THREE.Mesh>(null);

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();

    if (meshRef.current) {
      meshRef.current.rotation.y = t * 0.15;
      meshRef.current.rotation.x = Math.sin(t * 0.1) * 0.1;
    }

    // Inner core pulses
    if (innerRef.current) {
      const pulse = 0.4 + 0.15 * Math.sin(t * 1.5);
      const mat = innerRef.current.material as THREE.MeshBasicMaterial;
      mat.opacity = pulse;
      const s = 1 + 0.08 * Math.sin(t * 1.5);
      innerRef.current.scale.setScalar(s);
    }

    // Expanding pulse ring
    if (pulseRef.current) {
      const cycle = t % 4;
      const expanding = cycle < 1.5;
      const scale = expanding ? 1 + cycle * 1.5 : 3.25;
      pulseRef.current.scale.setScalar(scale);
      const mat = pulseRef.current.material as THREE.MeshBasicMaterial;
      mat.opacity = expanding ? 0.06 * (1 - cycle / 1.5) : 0;
    }
  });

  return (
    <group>
      {/* Outer wireframe vault */}
      <mesh ref={meshRef}>
        <dodecahedronGeometry args={[0.5, 0]} />
        <meshBasicMaterial
          color="#d4d4d8"
          transparent
          opacity={0.25}
          wireframe
        />
      </mesh>

      {/* Inner glowing core */}
      <mesh ref={innerRef}>
        <icosahedronGeometry args={[0.15, 1]} />
        <meshBasicMaterial color="#d4d4d8" transparent opacity={0.4} />
      </mesh>

      {/* Pulse ring */}
      <mesh ref={pulseRef} rotation={[-Math.PI / 2, 0, 0]}>
        <ringGeometry args={[0.5, 0.52, 48]} />
        <meshBasicMaterial
          color="#d4d4d8"
          transparent
          opacity={0}
          side={THREE.DoubleSide}
        />
      </mesh>
    </group>
  );
}

/* ─── Orbital ring made of dots ─── */
function OrbitalRing({
  radius,
  count,
  color,
  opacity,
  dotSize,
  tilt,
}: {
  radius: number;
  count: number;
  color: string;
  opacity: number;
  dotSize: number;
  tilt: [number, number, number];
}) {
  const dots = useMemo(() => {
    return Array.from({ length: count }, (_, i) => {
      const angle = (i / count) * Math.PI * 2;
      return [
        Math.cos(angle) * radius,
        Math.sin(angle) * radius,
      ] as [number, number];
    });
  }, [radius, count]);

  return (
    <group rotation={tilt}>
      {dots.map(([x, y], i) => (
        <mesh key={i} position={[x, 0, y]}>
          <sphereGeometry args={[dotSize, 6, 6]} />
          <meshBasicMaterial
            color={color}
            transparent
            opacity={opacity}
          />
        </mesh>
      ))}
    </group>
  );
}

/* ─── Orbiting token particles ($ORAMA ring) ─── */
function OramaOrbit() {
  const groupRef = useRef<THREE.Group>(null);
  const meshRefs = useRef<(THREE.Mesh | null)[]>([]);
  const count = 10;

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    meshRefs.current.forEach((mesh, i) => {
      if (!mesh) return;
      const angle = (i / count) * Math.PI * 2 + t * 0.3;
      mesh.position.x = Math.cos(angle) * ORAMA_RING_RADIUS;
      mesh.position.z = Math.sin(angle) * ORAMA_RING_RADIUS;
      mesh.position.y = Math.sin(t * 2 + i * 0.8) * 0.05;

      const mat = mesh.material as THREE.MeshBasicMaterial;
      const pulse = Math.sin(t * 1.5 - i * 0.6);
      mat.opacity = 0.3 + 0.4 * Math.max(0, pulse);
      const s = 1 + 0.3 * Math.max(0, pulse);
      mesh.scale.setScalar(s);
    });
  });

  return (
    <group ref={groupRef}>
      {Array.from({ length: count }, (_, i) => (
        <mesh
          key={i}
          ref={(el) => {
            meshRefs.current[i] = el;
          }}
        >
          <sphereGeometry args={[0.025, 10, 10]} />
          <meshBasicMaterial color="#d4d4d8" transparent opacity={0.4} />
        </mesh>
      ))}
    </group>
  );
}

/* ─── Orbiting token particles (Proxy ring — teal) ─── */
function ProxyOrbit() {
  const groupRef = useRef<THREE.Group>(null);
  const meshRefs = useRef<(THREE.Mesh | null)[]>([]);
  const count = 7;

  useFrame(({ clock }) => {
    const t = clock.getElapsedTime();
    meshRefs.current.forEach((mesh, i) => {
      if (!mesh) return;
      // Counter-rotate for visual contrast
      const angle = (i / count) * Math.PI * 2 - t * 0.4;
      mesh.position.x = Math.cos(angle) * PROXY_RING_RADIUS;
      mesh.position.z = Math.sin(angle) * PROXY_RING_RADIUS;
      mesh.position.y = Math.sin(t * 2.5 + i * 1.2) * 0.06;

      const mat = mesh.material as THREE.MeshBasicMaterial;
      const pulse = Math.sin(t * 2 + i * 0.9);
      mat.opacity = 0.4 + 0.3 * Math.max(0, pulse);
    });
  });

  return (
    <group ref={groupRef}>
      {Array.from({ length: count }, (_, i) => (
        <mesh
          key={i}
          ref={(el) => {
            meshRefs.current[i] = el;
          }}
        >
          <sphereGeometry args={[0.018, 8, 8]} />
          <meshBasicMaterial color="#00d4aa" transparent opacity={0.5} />
        </mesh>
      ))}
    </group>
  );
}

/* ─── Inflowing capital particles ─── */
function InflowParticles() {
  const ref = useRef<THREE.Points>(null);

  const { positions, velocities } = useMemo(() => {
    const pos = new Float32Array(PARTICLE_COUNT * 3);
    const vel = new Float32Array(PARTICLE_COUNT * 3);

    for (let i = 0; i < PARTICLE_COUNT; i++) {
      // Start at random positions on a large sphere
      const theta = Math.random() * Math.PI * 2;
      const phi = Math.acos(2 * Math.random() - 1);
      const r = 3 + Math.random() * 2;

      pos[i * 3] = r * Math.sin(phi) * Math.cos(theta);
      pos[i * 3 + 1] = r * Math.sin(phi) * Math.sin(theta);
      pos[i * 3 + 2] = r * Math.cos(phi);

      // Velocity toward center
      vel[i * 3] = -pos[i * 3] * 0.008;
      vel[i * 3 + 1] = -pos[i * 3 + 1] * 0.008;
      vel[i * 3 + 2] = -pos[i * 3 + 2] * 0.008;

    }

    return { positions: pos, velocities: vel };
  }, []);

  useFrame(({ clock }) => {
    if (!ref.current) return;
    const posAttr = ref.current.geometry.attributes
      .position as THREE.BufferAttribute;
    const arr = posAttr.array as Float32Array;
    const t = clock.getElapsedTime();

    for (let i = 0; i < PARTICLE_COUNT; i++) {
      const ix = i * 3;
      arr[ix] += velocities[ix];
      arr[ix + 1] += velocities[ix + 1];
      arr[ix + 2] += velocities[ix + 2];

      // Reset when close to center
      const dist = Math.sqrt(
        arr[ix] ** 2 + arr[ix + 1] ** 2 + arr[ix + 2] ** 2,
      );
      if (dist < 0.3) {
        const theta = Math.random() * Math.PI * 2;
        const phi = Math.acos(2 * Math.random() - 1);
        const r = 3 + Math.random() * 2;
        arr[ix] = r * Math.sin(phi) * Math.cos(theta);
        arr[ix + 1] = r * Math.sin(phi) * Math.sin(theta);
        arr[ix + 2] = r * Math.cos(phi);
        velocities[ix] = -arr[ix] * (0.006 + Math.random() * 0.004);
        velocities[ix + 1] = -arr[ix + 1] * (0.006 + Math.random() * 0.004);
        velocities[ix + 2] = -arr[ix + 2] * (0.006 + Math.random() * 0.004);
      }

      // Add slight spiral motion
      const spiral = 0.002;
      const cx = arr[ix];
      const cz = arr[ix + 2];
      arr[ix] += -cz * spiral;
      arr[ix + 2] += cx * spiral;
    }

    posAttr.needsUpdate = true;

    // Slow overall rotation
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
      <pointsMaterial
        size={0.015}
        color="#d4d4d8"
        transparent
        opacity={0.2}
        sizeAttenuation
      />
    </points>
  );
}

/* ─── Full scene composition ─── */
function GrowthVaultNetwork() {
  const groupRef = useRef<THREE.Group>(null);

  useFrame(({ clock }) => {
    if (groupRef.current) {
      groupRef.current.rotation.y = clock.getElapsedTime() * 0.03;
    }
  });

  return (
    <Float speed={0.5} rotationIntensity={0.01} floatIntensity={0.04}>
      <group ref={groupRef}>
        {/* Dot rings for orbit paths */}
        <OrbitalRing
          radius={ORAMA_RING_RADIUS}
          count={80}
          color="#a1a1aa"
          opacity={0.04}
          dotSize={0.004}
          tilt={[0, 0, 0]}
        />
        <OrbitalRing
          radius={PROXY_RING_RADIUS}
          count={50}
          color="#00d4aa"
          opacity={0.03}
          dotSize={0.003}
          tilt={[0.3, 0, 0.2]}
        />

        {/* Vault core */}
        <Vault />

        {/* Orbiting tokens */}
        <OramaOrbit />
        <group rotation={[0.3, 0, 0.2]}>
          <ProxyOrbit />
        </group>

        {/* Capital flowing in */}
        <InflowParticles />
      </group>
    </Float>
  );
}

export function GrowthVaultScene() {
  return (
    <div className="w-full h-[350px] md:h-[550px] -mt-[250px] md:-mt-[400px]">
      <Canvas
        camera={{ position: [0, 2, 3], fov: 40 }}
        dpr={[1, 2]}
        gl={{
          antialias: true,
          alpha: true,
          toneMapping: THREE.ACESFilmicToneMapping,
          toneMappingExposure: 1,
        }}
        style={{ background: "transparent" }}
      >
        <ambientLight intensity={0.1} />
        <GrowthVaultNetwork />
      </Canvas>
    </div>
  );
}
