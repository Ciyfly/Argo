()=>{
    function hook(){
        window.alert = function () { return false; };
        window.prompt = function (msg, input) { return input; };
        window.confirm = function () { return true; };
        window.close = function () { return false; };    
        window.open = function () { };
    }
    hook();
}


