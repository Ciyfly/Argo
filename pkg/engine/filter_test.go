package engine

import "testing"

func TestFilterStatic(t *testing.T) {
	InitFilter()
	target1 := "http://testphp.vulnweb.com/style.css"
	target2 := "http://testphp.vulnweb.com/hpp/params.php?p=valid&pp=12"
	target3 := "http://testphp.vulnweb.com/AJAX/styles.css#2378123687"
	target4 := "https://static.sj.qq.com/wupload/xy/yyb_official_website/ocgyts2d.png&#34;,&#34;alias&#34;:&#34;1671069624000&#34;,&#34;report_info&#34;:{&#34;cardid&#34;:&#34;YYB_HOME_GAME_DETAIL_RELATED_BLOG&#34;,&#34;slot&#34;:1}}],&#34;cardid&#34;:&#34;YYB_HOME_GAME_DETAIL_RELATED_BLOG&#34;},&#34;errors&#34;:[],&#34;report_info&#34;:{&#34;rel_exp_ids&#34;:&#34;&#34;,&#34;pos&#34;:5,&#34;offset&#34;:0}}],&#34;size&#34;:10,&#34;offset&#34;:0,&#34;total&#34;:5,&#34;exp_ids&#34;:&#34;&#34;,&#34;errors&#34;:[],&#34;version&#34;:&#34;20230324095500&#34;,&#34;report_info&#34;:{&#34;layout_scene&#34;:166,&#34;rel_exp_ids&#34;:&#34;&#34;}},&#34;msg&#34;:&#34;sucess&#34;},&#34;scene&#34;:&#34;game_detail&#34;,&#34;seoMeta&#34;:{&#34;keywords&#34;:&#34;火影忍者官方下载,火影忍者云游戏,火影忍者礼包码领取,火影忍者攻略&#34;,&#34;title&#34;:&#34;火影忍者官方下载-云游戏-攻略-礼包码-应用宝官网&#34;,&#34;description&#34;:&#34;应用宝为您提供ReplaceCurrentYear最新版火影忍者官方下载，火之意志，格斗重燃！《火影忍者》手游作为正版火影忍者格斗手游，由万代南梦宫授权、岸本齐史领衔集英社等版权方联合监修、腾讯游戏魔方工作室群自主研发而成。\\r\\n《火影忍者》手游100%正统还原原著剧情，疾风传篇章登场，十年百忍强力降临，玩家可以任意扮演鸣人、佐助、宇智波鼬等忍者，体验酣畅淋漓的忍术格斗连打和全屏奥义大招。此外，还可以进行跨服匹配2V2热血PK，参与无差别忍者格斗大赛，决出属于你的忍道！&#34;},&#34;breadcrumbItems&#34;:[{&#34;href&#34;:&#34;/&#34;},{&#34;name&#34;:&#34;火影忍者&#34;}]},&#34;__N_SSP&#34;:true},&#34;page&#34;:&#34;/appdetail/[pkgname]&#34;,&#34;query&#34;:{&#34;pkgname&#34;:&#34;com.tencent.KiHan&#34;},&#34;buildId&#34;:&#34;42xokfhjgxyYZj-94e1C5&#34;,&#34;assetPrefix&#34;:&#34;https://static.sj.qq.com&#34;,&#34;isFallback&#34;:false,&#34;dynamicIds&#34;:[17036],&#34;gssp&#34;:true,&#34;customServer&#34;:true,&#34;locale&#34;:&#34;zh&#34;,&#34;locales&#34;:[&#34;zh&#34;],&#34;defaultLocale&#34;:&#34;zh&#34;,&#34;scriptLoader&#34"
	if !filterStatic(target1) {
		t.Errorf("filterStatic fail: %s", target1)
	}
	if filterStatic(target2) {
		t.Errorf("filterStatic fail: %s", target1)
	}
	if !filterStatic(target3) {
		t.Errorf("filterStatic fail: %s", target1)
	}
	if !filterStatic(target4) {
		t.Errorf("filterStatic fail: %s", target1)
	}
}
