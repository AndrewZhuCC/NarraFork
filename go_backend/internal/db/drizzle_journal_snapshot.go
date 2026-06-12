package db

type drizzleMigrationSnapshotEntry struct {
	Tag  string
	When int64
	Hash string
}

var embeddedDrizzleMigrationSnapshot = []drizzleMigrationSnapshotEntry{
	{Tag: "0000_fine_genesis", When: 1773354892681, Hash: "a3afdf722b075718fbf39abe41d07f83e836cff89c3b89b72586167d5c51faaa"},
	{Tag: "0001_confused_jamie_braddock", When: 1773361658483, Hash: "73caa6e6ab4bb2fc59cea81b670fe96c547e88ae5fb80f513d406e321e6f8054"},
	{Tag: "0002_illegal_magma", When: 1773440433879, Hash: "2ea3def588d3797140fdbe2000f462ee3b2cb46f6ad53129c92101f6d03ac10c"},
	{Tag: "0003_dark_golden_guardian", When: 1774088175686, Hash: "ecc8f2d7245f4c87a6108d4dd25da582c673ab1586e1614ba977139d3babab01"},
	{Tag: "0004_rapid_banshee", When: 1774203321327, Hash: "b9742f10478b2e4d2900029da3d68a5b640adc43508a4b50bbb5bf1cd6dbe536"},
	{Tag: "0005_absurd_iron_fist", When: 1774334575741, Hash: "cd432a50905a4253fc97f27a50b80a9d6ac7b6d345e25cc4af1187281cadc0aa"},
	{Tag: "0006_wide_amphibian", When: 1774710263771, Hash: "886972ea2bc307c99cb8e36a34f7f93c26c36cad16dc78e33384302c2da5848c"},
	{Tag: "0007_fast_sheva_callister", When: 1775066274331, Hash: "71d44730be47f2144310b88d6d3f9e23635cdb28d9ae92b30d2f31b13938dbe4"},
	{Tag: "0008_breezy_lake", When: 1775413390804, Hash: "95727e15b025f65c67fd88a728c21c3323a73d41870fb61aa62771f3bc71e682"},
	{Tag: "0009_stormy_talos", When: 1775413546313, Hash: "1ed7cb0b08cab1ad9a60e7260d8966ae99cbbfc31680db0aa4b9406ceefc77ea"},
	{Tag: "0010_silky_obadiah_stane", When: 1775640975515, Hash: "0c2feeb92dde397ae35b0e2527700da4a0ee4f56c6f561d58f77d1af77eef002"},
	{Tag: "0011_messy_thor_girl", When: 1775781091529, Hash: "62c52bb111b2a4de8c61e63f1031a6a04cf108c716b4b811a30fa389cbb06e5b"},
	{Tag: "0012_furry_malcolm_colcord", When: 1776044397412, Hash: "c64a22a1ce7eb72493daf7e2139cadfd0e32daa92be2c59ee4af443c156225ee"},
	{Tag: "0013_slimy_naoko", When: 1776048735895, Hash: "8650bcdcf6ef1f4529b75dae87bf05d6ad1c05f68556b0a2c3a931d06103171f"},
	{Tag: "0014_shallow_thunderball", When: 1776050536104, Hash: "2d68306c9edc0b059da4f504c2cf1e2785208cbfa78d162ae3962979c320aa42"},
	{Tag: "0015_calm_the_executioner", When: 1776055303661, Hash: "5edbc58156276ac68c052a5a48868cee531bc9cb4c138dc57d6faae660c89f56"},
	{Tag: "0016_abandoned_the_anarchist", When: 1776071234371, Hash: "c06441944957ec854d7dfffa26a02b7957d26e759f85034e71b8cb468eafd1bb"},
	{Tag: "0017_breezy_shadowcat", When: 1776306142191, Hash: "f0f8c637cf4852519d7d2c922639d491707fecfe8015fccd85807a618a7e1ad2"},
	{Tag: "0018_warm_famine", When: 1776346847729, Hash: "50c7e64c8eca3732d2dc9a7b04a7a2ac8392d78c2e3ff838ec58ca6a65b91a88"},
	{Tag: "0019_sad_whiplash", When: 1776499027096, Hash: "311b003ffbb2579b421075185d2df848c9b9e2ee3faccba91ea08249de4b973a"},
	{Tag: "0020_add_im_gateway", When: 1776600000000, Hash: "40f02fa4870aacaa8afe46dded43e245abd7309d84f2299fa5ec3b562829f7cd"},
	{Tag: "0021_goofy_master_mold", When: 1776743388306, Hash: "9f12f77f2d251d0952ee32f4d8a17fdea863e24d4c2bb6bb8df4610b4f4ed18d"},
	{Tag: "0022_wet_sentry", When: 1776745313751, Hash: "a5b34c175758ee5efe29c70a910ef991cc550c78d22942e4d308cdbce1d9678d"},
	{Tag: "0023_colorful_leech", When: 1776757775009, Hash: "c83a99192de9be5da42d0e138d520511787ceb5d0588f6ef44d28d960a803166"},
	{Tag: "0024_outgoing_banshee", When: 1776783850961, Hash: "c078a12992253f44dca2a2ea00207f6beb1c71142d91156599b98563ed968c57"},
	{Tag: "0025_flawless_jazinda", When: 1776819422984, Hash: "28876b11a11fbb1d8034f503bc35011ad10495007ecb9dcbc2d2ef0cc64e5037"},
	{Tag: "0026_bright_bruce_banner", When: 1776838795525, Hash: "d8db2fac9d4825a453e151b85a24b3a1c17fa686118697ec40f73635329096a4"},
	{Tag: "0027_rare_maestro", When: 1776944112570, Hash: "605eae35f8b8500ac534f465b7c9ecf7e8a97bd218f8a2380e788a79753a4e9d"},
	{Tag: "0028_pale_silhouette", When: 1777102252438, Hash: "2c506a9c8c815225a688982aeec721274555c7809a7a6b28055acf8fcf70eea7"},
	{Tag: "0029_bizarre_maria_hill", When: 1777130172082, Hash: "859f391b5deea9f2c4bc824f9a5e5692889054aa2fb0182295388416854bbd5b"},
	{Tag: "0030_strange_triton", When: 1777340838468, Hash: "ea533e9b1150fec7973111c8e1e691de8eae7dc35e6253f1c113cefeb11918fb"},
	{Tag: "0031_sparkling_metal_master", When: 1777651658387, Hash: "7cf5a7cf76dde425a3e1917645076dfaacdfd41fdcb86b3b06de8cd5173b7920"},
	{Tag: "0032_absurd_switch", When: 1777733287812, Hash: "d085c4dbaa5734dc08b21123aea25b2a1a1f0414d269703ec3a0a196171a47b7"},
	{Tag: "0033_sticky_supreme_intelligence", When: 1777738395953, Hash: "f578c8bc1e56d3fbe91fa980dccc029dcd00d6fee6cb0f1314d5520a77f64ce7"},
	{Tag: "0034_odd_landau", When: 1777741351829, Hash: "390ce722ebf917f885a485a0d6c38872f4bfd761dbf04319a27bd68b2b9b9d30"},
	{Tag: "0035_grey_deathstrike", When: 1777831004202, Hash: "197845b08f3555f31c786936f2fea2639061db5aeaed537ff478bd0ea179ed59"},
	{Tag: "0036_gigantic_exiles", When: 1777960781882, Hash: "86b467f86e202a81577beb8e3580525cd3b0b2ff965e12d7a23018e5e08212e3"},
	{Tag: "0037_bouncy_junta", When: 1778030576561, Hash: "7f05ab4983c6641328d6da6a9e5339726df1cddae5be72e5b30f0fcdba183d30"},
	{Tag: "0038_spotty_metal_master", When: 1778067344612, Hash: "6b4dd65908dfe0ae4b7173fae738b7998867f232cd9078bf8328c51dd592d195"},
	{Tag: "0039_mature_reptil", When: 1778258355695, Hash: "2da342ecb4ab254d8e46fa428ae38b346c71c3b5c82122aa7e4f737c4459e237"},
	{Tag: "0040_talented_pandemic", When: 1778483346461, Hash: "b110cd1c783f2959b91322c23ebae9d239215d3321fbb725b3aa90955af8f81e"},
	{Tag: "0041_exotic_nekra", When: 1781187476955, Hash: "5b6892b1717ea94d0d6768c9ee9765200662cfc2dedccc6a8b87b69f11f68d7c"},
	{Tag: "0042_shocking_naoko", When: 1781278512923, Hash: "a905aa05262b28902f5e8873004172e0e6237b8c400ec4f959b5c4c4a2a5460a"},
}
