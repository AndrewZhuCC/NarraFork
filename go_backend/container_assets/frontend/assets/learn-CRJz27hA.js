import{a as e}from"./rolldown-runtime-BYbx6iT9.js";import{n as t,t as n}from"./jsx-runtime-B6ABtYIT.js";import{a as r}from"./DirectionProvider-ByY3_EfP.js";import{t as i}from"./ScrollArea-DDuijA-f.js";import{t as a}from"./Paper-BMS2Mqe2.js";import{t as o}from"./Loader-D7sHUN_8.js";import{t as s}from"./Group-BLm3FcgS.js";import{t as c}from"./Text-C5W7zxnY.js";import{t as l}from"./Button-B31DQSPE.js";import{t as u}from"./Card-C9wM-Gbk.js";import{t as d}from"./Divider-D2ULFdvj.js";import{t as f}from"./Grid-BYHOOdre.js";import{t as p}from"./Stack-CKKKPJ5L.js";import{t as m}from"./ThemeIcon-f3SRX-Iv.js";import{t as h}from"./useTranslation-BLm0uO1N.js";import{t as g}from"./useQuery-BNWR0zOW.js";import{t as _}from"./createReactComponent-CJTJT963.js";import{t as v}from"./IconArrowRight-CW8gpegm.js";import{t as y}from"./IconChevronDown-CukVJUB-.js";import{t as b}from"./IconChevronRight-BJxj577y.js";import{t as x}from"./IconInfoCircle-DIvTfR6f.js";import{t as S}from"./IconSparkles-0syMggse.js";import{t as C}from"./api-033Bd-VI.js";import{Gn as w,On as T,Un as E,Xn as D,Yn as O,nr as k,s as A,zn as j}from"./index-NEuFTu6V.js";var M=_(`outline`,`bulb`,`Bulb`,[[`path`,{d:`M3 12h1m8 -9v1m8 8h1m-15.4 -6.4l.7 .7m12.1 -.7l-.7 .7`,key:`svg-0`}],[`path`,{d:`M9 16a5 5 0 1 1 6 0a3.5 3.5 0 0 0 -1 3a2 2 0 0 1 -4 0a3.5 3.5 0 0 0 -1 -3`,key:`svg-1`}],[`path`,{d:`M9.7 17l4.6 0`,key:`svg-2`}]]),N=_(`outline`,`checklist`,`Checklist`,[[`path`,{d:`M9.615 20h-2.615a2 2 0 0 1 -2 -2v-12a2 2 0 0 1 2 -2h8a2 2 0 0 1 2 2v8`,key:`svg-0`}],[`path`,{d:`M14 19l2 2l4 -4`,key:`svg-1`}],[`path`,{d:`M9 8h4`,key:`svg-2`}],[`path`,{d:`M9 12h2`,key:`svg-3`}]]),P=e(t()),F=n();function I(e){return e?.toLowerCase().startsWith(`zh`)?`zh-CN`:`en`}var L=6e4;function R(){let{t:e,i18n:t}=h(`learning`),n=A.useSearch(),_=A.useNavigate(),y=I(t.resolvedLanguage??t.language),b=n.q??``,[x,S]=(0,P.useState)(()=>new Set),{data:j,isLoading:R}=g({queryKey:[`learning`,`index`,y],queryFn:()=>C.getLearningIndex(y),gcTime:L}),B=(0,P.useMemo)(()=>{let e=j?.docs??[],t=b.trim().toLowerCase();return e.filter(e=>t?[e.title,e.summary,e.id,e.category,...e.tags].join(`
`).toLowerCase().includes(t):!0)},[j?.docs,b]),H=(0,P.useMemo)(()=>{if(!j)return[];let e=new Map;for(let t of B){let n=e.get(t.category)??[];n.push(t),e.set(t.category,n)}let t=new Set(j.categories.map(e=>e.id)),n=j.categories.map(t=>({category:t,docs:e.get(t.id)??[]}));for(let[r,i]of e)t.has(r)||n.push({category:{id:r,label:r,description:``},docs:i});return n.filter(e=>e.docs.length>0)},[B,j]),U=n.doc??B[0]?.id,W=(B.find(e=>e.id===U)??B[0])?.id;(0,P.useEffect)(()=>{!j||!n.doc||j.docs.some(e=>e.id===n.doc)||_({search:e=>({...e,doc:void 0})})},[j,_,n.doc]);let{data:G,isLoading:K}=g({queryKey:[`learning`,`doc`,W,y],queryFn:()=>C.getLearningDoc(W??``,y),enabled:!!W,gcTime:L}),q=e=>{_({search:t=>({...t,doc:e})})},J=e=>{_({search:t=>({...t,q:e||void 0,doc:void 0})})},Y=e=>{S(t=>{let n=new Set(t);return n.has(e)?n.delete(e):n.add(e),n})};return R?(0,F.jsx)(o,{}):(0,F.jsxs)(p,{gap:`md`,className:`nf-blur-in-enter`,children:[(0,F.jsx)(a,{withBorder:!0,p:`xl`,radius:`lg`,className:`learn-hero`,children:(0,F.jsxs)(s,{gap:`md`,align:`flex-start`,wrap:`nowrap`,children:[(0,F.jsx)(m,{variant:`gradient`,gradient:{from:`indigo`,to:`violet`},size:`xl`,children:(0,F.jsx)(E,{size:24})}),(0,F.jsxs)(r,{maw:760,children:[(0,F.jsx)(O,{order:1,lh:1.1,children:e(`title`)}),(0,F.jsx)(c,{c:`dimmed`,mt:`sm`,size:`lg`,maw:680,children:e(`subtitle`)})]})]})}),(0,F.jsxs)(f,{gutter:`md`,children:[(0,F.jsx)(f.Col,{span:{base:12,md:4},children:(0,F.jsxs)(u,{withBorder:!0,p:0,radius:`lg`,className:`learn-panel learn-doc-list`,children:[(0,F.jsx)(r,{p:`md`,pb:`sm`,children:(0,F.jsx)(D,{leftSection:(0,F.jsx)(T,{size:16}),placeholder:e(`searchPlaceholder`),value:b,onChange:e=>J(e.currentTarget.value)})}),(0,F.jsx)(i,{h:640,children:(0,F.jsx)(p,{gap:2,px:`xs`,pb:`xs`,children:B.length===0?(0,F.jsx)(c,{c:`dimmed`,p:`md`,children:e(`noResults`)}):H.map(({category:e,docs:t})=>(0,F.jsx)(z,{category:e,docs:t,opened:t.some(e=>e.id===W)||!x.has(e.id),activeDocId:W,onToggle:()=>Y(e.id),onSelectDoc:q},e.id))})})]})}),(0,F.jsx)(f.Col,{span:{base:12,md:8},children:(0,F.jsx)(u,{withBorder:!0,p:`xl`,radius:`lg`,className:`learn-panel learn-content`,children:K?(0,F.jsx)(o,{}):G?(0,F.jsxs)(p,{gap:`lg`,children:[(0,F.jsxs)(r,{children:[(0,F.jsx)(s,{gap:`xs`,mb:`sm`,children:G.tags.map(e=>(0,F.jsx)(k,{variant:`light`,color:`gray`,children:e},e))}),(0,F.jsx)(O,{order:2,children:G.title}),(0,F.jsx)(c,{c:`dimmed`,mt:`xs`,children:G.summary})]}),G.actions.length>0&&(0,F.jsxs)(a,{withBorder:!0,p:`md`,radius:`md`,className:`learn-callout`,children:[(0,F.jsx)(c,{fw:700,mb:`sm`,children:e(`jumpToFeature`)}),(0,F.jsx)(s,{gap:`sm`,children:G.actions.map(e=>(0,F.jsx)(l,{component:`a`,href:e.href,variant:`light`,rightSection:(0,F.jsx)(v,{size:14}),children:e.label},`${e.href}:${e.label}`))})]}),(0,F.jsx)(d,{}),G.sections.map(e=>(0,F.jsxs)(r,{children:[(0,F.jsx)(O,{order:3,mb:`xs`,children:e.title}),(0,F.jsx)(c,{children:e.body})]},e.title)),(0,F.jsx)(V,{icon:(0,F.jsx)(N,{size:18}),title:e(`workflow`),items:G.workflow,ordered:!0}),(0,F.jsx)(V,{icon:(0,F.jsx)(M,{size:18}),title:e(`bestPractices`),items:G.bestPractices}),(0,F.jsx)(V,{icon:(0,F.jsx)(w,{size:18}),title:e(`pitfalls`),items:G.pitfalls,color:`orange`})]}):(0,F.jsx)(c,{c:`dimmed`,children:e(`selectDoc`)})})})]}),(0,F.jsx)(`style`,{children:`
				.learn-hero {
					position: relative;
					overflow: hidden;
					background:
						radial-gradient(circle at top left, color-mix(in srgb, var(--mantine-color-indigo-6) 18%, transparent), transparent 34rem),
						radial-gradient(circle at 85% 10%, color-mix(in srgb, var(--mantine-color-violet-6) 14%, transparent), transparent 26rem),
						light-dark(var(--mantine-color-white), var(--mantine-color-dark-7));
					border-color: light-dark(var(--mantine-color-indigo-1), var(--mantine-color-dark-4));
				}

				.learn-panel {
					background: light-dark(var(--mantine-color-white), var(--mantine-color-dark-7));
					border-color: light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4));
					box-shadow: 0 10px 30px light-dark(rgba(15, 23, 42, 0.06), rgba(0, 0, 0, 0.22));
				}

				.learn-content {
					background:
						linear-gradient(180deg, color-mix(in srgb, var(--mantine-color-indigo-6) 5%, transparent), transparent 12rem),
						light-dark(var(--mantine-color-white), var(--mantine-color-dark-7));
				}

				.learn-callout {
					background: light-dark(var(--mantine-color-indigo-0), color-mix(in srgb, var(--mantine-color-indigo-9) 24%, var(--mantine-color-dark-7)));
					border-color: light-dark(var(--mantine-color-indigo-2), var(--mantine-color-indigo-9));
				}

				.learn-tree-children {
					margin-left: 18px;
					padding-left: 8px;
					border-left: 1px dashed light-dark(var(--mantine-color-gray-3), var(--mantine-color-dark-4));
				}

				.learn-category,
				.learn-doc-item {
					border-radius: var(--mantine-radius-sm);
					transition: background-color 120ms ease, border-color 120ms ease, box-shadow 120ms ease;
				}

				.learn-category:hover,
				.learn-doc-item:hover {
					background: light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-6));
				}

				.learn-category[data-active="true"],
				.learn-doc-item[data-active="true"] {
					background: linear-gradient(90deg, var(--mantine-color-indigo-light), transparent);
					border-color: var(--mantine-color-indigo-5);
				}
			`})]})}function z({category:e,docs:t,opened:n,activeDocId:i,onToggle:a,onSelectDoc:o}){return(0,F.jsxs)(r,{className:`learn-tree-group`,children:[(0,F.jsx)(r,{component:`button`,type:`button`,className:`learn-category`,"data-active":t.some(e=>e.id===i),onClick:a,style:{width:`100%`,border:0,background:`transparent`,color:`inherit`,cursor:`pointer`,textAlign:`left`,padding:`8px 10px`},children:(0,F.jsxs)(s,{gap:`xs`,wrap:`nowrap`,children:[(0,F.jsx)(m,{variant:`subtle`,color:`indigo`,size:`sm`,children:n?(0,F.jsx)(y,{size:14}):(0,F.jsx)(b,{size:14})}),(0,F.jsx)(j,{size:16,color:`var(--mantine-color-indigo-4)`}),(0,F.jsxs)(r,{style:{flex:1,minWidth:0},children:[(0,F.jsxs)(s,{gap:6,wrap:`nowrap`,children:[(0,F.jsx)(c,{fw:700,size:`sm`,lineClamp:1,children:e.label}),(0,F.jsx)(k,{size:`xs`,variant:`light`,color:`gray`,children:t.length})]}),e.description&&(0,F.jsx)(c,{c:`dimmed`,size:`xs`,lineClamp:1,children:e.description})]})]})}),n&&(0,F.jsx)(p,{gap:1,className:`learn-tree-children`,children:t.map(e=>(0,F.jsx)(B,{doc:e,active:e.id===i,onClick:()=>o(e.id)},e.id))})]})}function B({doc:e,active:t,onClick:n}){return(0,F.jsx)(r,{component:`button`,type:`button`,className:`learn-doc-item`,"data-active":t,onClick:n,style:{width:`100%`,border:0,borderLeft:`3px solid transparent`,borderBottom:`1px solid var(--mantine-color-default-border)`,background:`transparent`,color:`inherit`,cursor:`pointer`,textAlign:`left`,padding:`8px 10px`},children:(0,F.jsxs)(s,{wrap:`nowrap`,align:`flex-start`,gap:`xs`,children:[(0,F.jsx)(m,{variant:`light`,color:t?`indigo`:`gray`,size:`sm`,children:(0,F.jsx)(x,{size:14})}),(0,F.jsxs)(r,{style:{flex:1,minWidth:0},children:[(0,F.jsx)(c,{fw:700,size:`sm`,children:e.title}),(0,F.jsx)(c,{size:`xs`,c:`dimmed`,lineClamp:2,children:e.summary}),(0,F.jsx)(s,{gap:4,mt:`xs`,children:e.tags.slice(0,3).map(e=>(0,F.jsx)(k,{size:`xs`,variant:`outline`,color:`gray`,children:e},e))})]})]})})}function V({icon:e,title:t,items:n,ordered:r=!1,color:i=`indigo`}){return n.length===0?null:(0,F.jsxs)(a,{withBorder:!0,p:`md`,radius:`md`,className:`learn-callout`,children:[(0,F.jsxs)(s,{gap:`xs`,mb:`sm`,children:[(0,F.jsx)(m,{variant:`light`,color:i,children:e}),(0,F.jsx)(c,{fw:700,children:t})]}),(0,F.jsx)(p,{gap:`xs`,children:n.map((e,t)=>(0,F.jsxs)(s,{align:`flex-start`,wrap:`nowrap`,gap:`sm`,children:[(0,F.jsx)(m,{variant:`subtle`,color:i,size:`sm`,children:r?t+1:(0,F.jsx)(S,{size:12})}),(0,F.jsx)(c,{size:`sm`,children:e})]},e))})]})}export{R as component};