<template>
  <svg viewBox="0 0 1024 1024" version="1.1" xmlns="http://www.w3.org/2000/svg" v-bind="$attrs" aria-hidden="true">
    <defs>
      <linearGradient id="agenthub_logo_bg" x1="0%" y1="0%" x2="0%" y2="100%">
        <stop offset="0%" stop-color="#FFFFFF" />
        <stop offset="100%" stop-color="#EDF2F5" />
      </linearGradient>
    </defs>

    <rect x="0" y="0" width="1024" height="1024" rx="160" fill="url(#agenthub_logo_bg)" />

    <!-- antenna -->
    <rect x="492" y="280" width="40" height="100" rx="20" fill="#1E293B" />
    <circle cx="512" cy="236" r="54" fill="#10B981" />

    <!-- head -->
    <rect x="248" y="370" width="528" height="460" rx="116" fill="#1E293B" />
    <rect x="184" y="520" width="48" height="180" rx="24" fill="#1E293B" />
    <rect x="792" y="520" width="48" height="180" rx="24" fill="#1E293B" />

    <!-- eyes -->
    <circle cx="400" cy="600" r="74" fill="#10B981" />
    <circle cx="624" cy="600" r="74" fill="#10B981" />
  </svg>
</template>
